#include <wasm.h>
#include <wasmtime.h>

wasmtime_store_t *go_store_new(wasm_engine_t *engine, size_t env);
void go_func_new(wasmtime_context_t *context, wasm_functype_t *ty, size_t env, int wrap,  wasmtime_func_t *ret);
wasmtime_error_t *go_linker_define_func(
    wasmtime_linker_t *linker,
    const char *module,
    size_t module_len,
    const char *name,
    size_t name_len,
    const wasm_functype_t *ty,
    int wrap,
    size_t env
);

#define EACH_UNION_ACCESSOR(name) \
  UNION_ACCESSOR(wasmtime_val, i32, int32_t) \
  UNION_ACCESSOR(wasmtime_val, i64, int64_t) \
  UNION_ACCESSOR(wasmtime_val, f32, float) \
  UNION_ACCESSOR(wasmtime_val, f64, double) \
  UNION_ACCESSOR(wasmtime_val, funcref, wasmtime_func_t) \
  \
  UNION_ACCESSOR(wasmtime_extern, func, wasmtime_func_t) \
  UNION_ACCESSOR(wasmtime_extern, memory, wasmtime_memory_t) \
  UNION_ACCESSOR(wasmtime_extern, table, wasmtime_table_t) \
  UNION_ACCESSOR(wasmtime_extern, global, wasmtime_global_t)

#define UNION_ACCESSOR(name, field, ty) \
  ty go_##name##_##field##_get(const name##_t *val); \
  void go_##name##_##field##_set(name##_t *val, ty i);

EACH_UNION_ACCESSOR(UNION_ACCESSOR)

#undef UNION_ACCESSOR

#ifdef WASMTIME_FEATURE_COMPONENT_MODEL_ASYNC
typedef struct go_component_async_state go_component_async_state_t;
wasmtime_error_t *go_component_linker_instance_add_func_async(
    wasmtime_component_linker_instance_t *instance,
    const char *name,
    size_t name_len,
    size_t env
);
wasmtime_error_t *go_wasmtime_error_new(const char *message, size_t message_len);
size_t go_component_async_state_callback(const go_component_async_state_t *state);
wasmtime_context_t *go_component_async_state_context(const go_component_async_state_t *state);
const wasmtime_component_func_type_t *go_component_async_state_func_type(const go_component_async_state_t *state);
wasmtime_component_val_t *go_component_async_state_args(const go_component_async_state_t *state);
size_t go_component_async_state_nargs(const go_component_async_state_t *state);
wasmtime_component_val_t *go_component_async_state_results(const go_component_async_state_t *state);
size_t go_component_async_state_nresults(const go_component_async_state_t *state);
bool go_component_async_state_cancelled(const go_component_async_state_t *state);
#endif
