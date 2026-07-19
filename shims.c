#include "_cgo_export.h"
#include "shims.h"
#include <stdlib.h>
#include <string.h>

wasmtime_store_t *go_store_new(wasm_engine_t *engine, size_t env) {
  return wasmtime_store_new(engine, (void*) env, goFinalizeStore);
}

static wasm_trap_t* trampoline(
   void *env,
   wasmtime_caller_t *caller,
   const wasmtime_val_t *args,
   size_t nargs,
   wasmtime_val_t *results,
   size_t nresults
) {
    return goTrampolineNew(caller, (size_t) env,
        (wasmtime_val_t*) args, nargs,
        results, nresults);
}

static wasm_trap_t* wrap_trampoline(
   void *env,
   wasmtime_caller_t *caller,
   const wasmtime_val_t *args,
   size_t nargs,
   wasmtime_val_t *results,
   size_t nresults
) {
    return goTrampolineWrap(caller, (size_t) env,
        (wasmtime_val_t*) args, nargs,
        results, nresults);
}

void go_func_new(
    wasmtime_context_t *store,
    wasm_functype_t *ty,
    size_t env,
    int wrap,
    wasmtime_func_t *ret
) {
  wasmtime_func_callback_t callback = trampoline;
  if (wrap)
    callback = wrap_trampoline;
  return wasmtime_func_new(store, ty, callback, (void*) env, NULL, ret);
}

wasmtime_error_t *go_linker_define_func(
    wasmtime_linker_t *linker,
    const char *module,
    size_t module_len,
    const char *name,
    size_t name_len,
    const wasm_functype_t *ty,
    int wrap,
    size_t env
) {
  wasmtime_func_callback_t cb = trampoline;
  void(*finalizer)(void*) = goFinalizeFuncNew;
  if (wrap) {
    cb = wrap_trampoline;
    finalizer = goFinalizeFuncWrap;
  }
  return wasmtime_linker_define_func(linker, module, module_len, name, name_len, ty, cb, (void*) env, finalizer);
}

#define UNION_ACCESSOR(name, field, ty) \
  ty go_##name##_##field##_get(const name##_t *val) { return val->of.field; } \
  void go_##name##_##field##_set(name##_t *val, ty i) { val->of.field = i; }

EACH_UNION_ACCESSOR(UNION_ACCESSOR)

#ifdef WASMTIME_FEATURE_COMPONENT_MODEL_ASYNC
#include <stdatomic.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

struct go_component_async_state {
  atomic_int references;
  atomic_bool ready;
  atomic_bool cancelled;
  size_t callback;
  uintptr_t context_key;
  wasmtime_component_func_type_t *func_type;
  wasmtime_component_async_waker_t *waker;
  wasmtime_component_val_t *args;
  size_t nargs;
  wasmtime_component_val_t *results;
  wasmtime_component_val_t *staged_results;
  size_t nresults;
};

static void component_async_state_release(go_component_async_state_t *state) {
  if (atomic_fetch_sub_explicit(&state->references, 1, memory_order_acq_rel) != 1) return;
  for (size_t i = 0; i < state->nargs; i++) {
    wasmtime_component_val_delete(&state->args[i]);
  }
  free(state->args);
  for (size_t i = 0; i < state->nresults; i++) {
    wasmtime_component_val_delete(&state->staged_results[i]);
  }
  free(state->staged_results);
  wasmtime_component_func_type_delete(state->func_type);
  if (state->waker != NULL) wasmtime_component_async_waker_delete(state->waker);
  free(state);
}

#ifdef _WIN32
static DWORD WINAPI component_async_worker(LPVOID raw) {
  go_component_async_state_t *state = raw;
  goComponentAsyncWorker(state);
  atomic_store_explicit(&state->ready, true, memory_order_release);
  wasmtime_component_async_waker_wake(state->waker);
  component_async_state_release(state);
  return 0;
}
static bool component_async_start_worker(go_component_async_state_t *state) {
  HANDLE thread = CreateThread(NULL, 0, component_async_worker, state, 0, NULL);
  if (thread == NULL) return false;
  CloseHandle(thread);
  return true;
}
#else
static void *component_async_worker(void *raw) {
  go_component_async_state_t *state = raw;
  goComponentAsyncWorker(state);
  atomic_store_explicit(&state->ready, true, memory_order_release);
  wasmtime_component_async_waker_wake(state->waker);
  component_async_state_release(state);
  return NULL;
}
static bool component_async_start_worker(go_component_async_state_t *state) {
  pthread_t thread;
  if (pthread_create(&thread, NULL, component_async_worker, state) != 0) return false;
  pthread_detach(thread);
  return true;
}
#endif

static bool component_async_continuation_poll(void *raw) {
  go_component_async_state_t *state = raw;
  if (!atomic_load_explicit(&state->ready, memory_order_acquire)) return false;
  for (size_t i = 0; i < state->nresults; i++) {
    wasmtime_component_val_clone(&state->staged_results[i], &state->results[i]);
  }
  return true;
}

static void component_async_continuation_finalize(void *raw) {
  go_component_async_state_t *state = raw;
  atomic_store_explicit(&state->cancelled, true, memory_order_release);
  component_async_state_release(state);
}

static void component_async_trampoline(
    void *env,
    wasmtime_context_t *context,
    const wasmtime_component_func_type_t *ty,
    wasmtime_component_val_t *args,
    size_t nargs,
    wasmtime_component_val_t *results,
    size_t nresults,
    wasmtime_error_t **error_ret,
    const wasmtime_component_async_waker_t *waker,
    wasmtime_async_continuation_t *continuation_ret
) {
  go_component_async_state_t *state = calloc(1, sizeof(go_component_async_state_t));
  if (state == NULL) {
    *error_ret = wasmtime_error_new("failed to allocate async component callback state");
    return;
  }
  atomic_init(&state->references, 2);
  atomic_init(&state->ready, false);
  atomic_init(&state->cancelled, false);
  state->callback = (size_t)env;
  state->context_key = (uintptr_t)context;
  state->func_type = wasmtime_component_func_type_clone(ty);
  state->waker = wasmtime_component_async_waker_clone(waker);
  state->nargs = nargs;
  state->results = results;
  state->nresults = nresults;
  if (state->func_type == NULL) {
    wasmtime_component_async_waker_delete(state->waker);
    free(state);
    *error_ret = wasmtime_error_new("failed to clone async component function type");
    return;
  }
  if (nresults > 0) {
    state->staged_results = calloc(nresults, sizeof(wasmtime_component_val_t));
    if (state->staged_results == NULL) {
      wasmtime_component_func_type_delete(state->func_type);
      wasmtime_component_async_waker_delete(state->waker);
      free(state);
      *error_ret = wasmtime_error_new("failed to allocate async component results");
      return;
    }
  }
  if (nargs > 0) {
    state->args = calloc(nargs, sizeof(wasmtime_component_val_t));
    if (state->args == NULL) {
      free(state->staged_results);
      wasmtime_component_func_type_delete(state->func_type);
      wasmtime_component_async_waker_delete(state->waker);
      free(state);
      *error_ret = wasmtime_error_new("failed to allocate async component arguments");
      return;
    }
    for (size_t i = 0; i < nargs; i++) {
      wasmtime_component_val_clone(&args[i], &state->args[i]);
    }
  }
  if (!component_async_start_worker(state)) {
    atomic_store_explicit(&state->references, 1, memory_order_relaxed);
    component_async_state_release(state);
    *error_ret = wasmtime_error_new("failed to start async component worker thread");
    return;
  }
  continuation_ret->callback = component_async_continuation_poll;
  continuation_ret->env = state;
  continuation_ret->finalizer = component_async_continuation_finalize;
}

#ifdef _WIN32
static DWORD WINAPI component_async_func_finalizer_worker(LPVOID raw) {
  goFinalizeComponentAsyncFunc((void *)(uintptr_t)raw);
  return 0;
}
static void component_async_func_finalize(void *env) {
  HANDLE thread = CreateThread(NULL, 0, component_async_func_finalizer_worker, env, 0, NULL);
  if (thread != NULL) CloseHandle(thread);
}
#else
static void *component_async_func_finalizer_worker(void *raw) {
  goFinalizeComponentAsyncFunc(raw);
  return NULL;
}
static void component_async_func_finalize(void *env) {
  pthread_t thread;
  if (pthread_create(&thread, NULL, component_async_func_finalizer_worker, env) == 0) {
    pthread_detach(thread);
  }
}
#endif

wasmtime_error_t *go_component_linker_instance_add_func_async(
    wasmtime_component_linker_instance_t *instance,
    const char *name,
    size_t name_len,
    size_t env
) {
#if defined(__linux__) && defined(__x86_64__)
  return wasmtime_component_linker_instance_add_func_concurrent(
      instance, name, name_len, component_async_trampoline, (void *)env,
      component_async_func_finalize);
#else
  (void)instance; (void)name; (void)name_len; (void)env;
  return wasmtime_error_new("concurrent Component Model callbacks are unavailable for this platform archive");
#endif
}

wasmtime_call_future_t *go_component_func_call_concurrent_async(
    const wasmtime_component_func_t *func,
    wasmtime_context_t *context,
    const wasmtime_component_val_t *args,
    size_t args_size,
    wasmtime_component_val_t *results,
    size_t results_size,
    wasmtime_error_t **error_ret
) {
#if defined(__linux__) && defined(__x86_64__)
  return wasmtime_component_func_call_concurrent_async(
      func, context, args, args_size, results, results_size, error_ret);
#else
  (void)func; (void)context; (void)args; (void)args_size;
  (void)results; (void)results_size;
  *error_ret = wasmtime_error_new("concurrent Component Model calls are unavailable for this platform archive");
  return NULL;
#endif
}

wasmtime_error_t *go_wasmtime_error_new(const char *message, size_t message_len) {
  char *copy = malloc(message_len + 1);
  if (copy == NULL) return wasmtime_error_new("failed to allocate error message");
  memcpy(copy, message, message_len);
  copy[message_len] = '\0';
  wasmtime_error_t *error = wasmtime_error_new(copy);
  free(copy);
  return error;
}

size_t go_component_async_state_callback(const go_component_async_state_t *state) { return state->callback; }
size_t go_component_async_state_context_key(const go_component_async_state_t *state) { return state->context_key; }
const wasmtime_component_func_type_t *go_component_async_state_func_type(const go_component_async_state_t *state) { return state->func_type; }
wasmtime_component_val_t *go_component_async_state_args(const go_component_async_state_t *state) { return state->args; }
size_t go_component_async_state_nargs(const go_component_async_state_t *state) { return state->nargs; }
wasmtime_component_val_t *go_component_async_state_results(const go_component_async_state_t *state) { return state->staged_results; }
size_t go_component_async_state_nresults(const go_component_async_state_t *state) { return state->nresults; }
bool go_component_async_state_cancelled(const go_component_async_state_t *state) {
  return atomic_load_explicit(&state->cancelled, memory_order_acquire);
}
#endif
