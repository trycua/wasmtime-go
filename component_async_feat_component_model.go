package wasmtime

// #include "shims.h"
// #include <stdlib.h>
//
// static inline wasmtime_component_val_t *go_component_val_array_new(size_t n) {
//   return n == 0 ? NULL : calloc(n, sizeof(wasmtime_component_val_t));
// }
// static inline wasmtime_component_val_t *go_component_val_array_nth(wasmtime_component_val_t *values, size_t i) {
//   return &values[i];
// }
// static inline void go_component_val_array_delete(wasmtime_component_val_t *values, size_t n) {
//   if (values == NULL) return;
//   for (size_t i = 0; i < n; i++) wasmtime_component_val_delete(&values[i]);
//   free(values);
// }
// static inline wasmtime_error_t **go_component_error_slot_new(void) {
//   return calloc(1, sizeof(wasmtime_error_t *));
// }
// static inline wasmtime_error_t *go_component_error_slot_get(wasmtime_error_t **slot) { return *slot; }
// static inline void go_component_error_slot_delete(wasmtime_error_t **slot) { free(slot); }
// static inline wasmtime_component_instance_t *go_component_instance_slot_new(void) {
//   return calloc(1, sizeof(wasmtime_component_instance_t));
// }
// static inline void go_component_instance_slot_delete(wasmtime_component_instance_t *slot) { free(slot); }
// static inline wasmtime_component_func_t *go_component_func_copy(const wasmtime_component_func_t *func) {
//   wasmtime_component_func_t *copy = malloc(sizeof(wasmtime_component_func_t));
//   if (copy != NULL) *copy = *func;
//   return copy;
// }
// static inline void go_component_func_copy_delete(wasmtime_component_func_t *func) { free(func); }
// static inline const wasmtime_component_result_type_t *go_async_valtype_result(const wasmtime_component_valtype_t *ty) { return ty->of.result; }
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

// ComponentAsyncFunc is a component host function whose work may suspend.
// The callback runs in its own goroutine and should honor ctx cancellation.
type ComponentAsyncFunc func(ctx context.Context, args []interface{}) ([]interface{}, error)

// ErrComponentHostPanic identifies a panic recovered from an async host callback.
var ErrComponentHostPanic = errors.New("component host callback panicked")

type componentCallState struct {
	ctx context.Context

	mu  sync.Mutex
	err error
}

func (state *componentCallState) recordError(err error) {
	if err == nil {
		return
	}
	state.mu.Lock()
	if state.err == nil {
		state.err = err
	}
	state.mu.Unlock()
}

func (state *componentCallState) resultError() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.err
}

var componentCalls = struct {
	sync.Mutex
	byContext map[uintptr]*componentCallState
}{byContext: make(map[uintptr]*componentCallState)}

func componentContextKey(ctx *C.wasmtime_context_t) uintptr {
	return uintptr(unsafe.Pointer(ctx))
}

func beginComponentCall(ctx *C.wasmtime_context_t, goContext context.Context) (*componentCallState, error) {
	if goContext == nil {
		goContext = context.Background()
	}
	key := componentContextKey(ctx)
	componentCalls.Lock()
	defer componentCalls.Unlock()
	if _, exists := componentCalls.byContext[key]; exists {
		return nil, fmt.Errorf("a component call future is already active for this store")
	}
	state := &componentCallState{ctx: goContext}
	componentCalls.byContext[key] = state
	return state, nil
}

func endComponentCall(ctx *C.wasmtime_context_t, state *componentCallState) {
	key := componentContextKey(ctx)
	componentCalls.Lock()
	if componentCalls.byContext[key] == state {
		delete(componentCalls.byContext, key)
	}
	componentCalls.Unlock()
}

func activeComponentCallKey(key uintptr) *componentCallState {
	componentCalls.Lock()
	state := componentCalls.byContext[key]
	componentCalls.Unlock()
	return state
}

func writeComponentAsyncResults(
	callState *componentCallState,
	funcType *C.wasmtime_component_func_type_t,
	results *C.wasmtime_component_val_t,
	nresults int,
	values []interface{},
	callbackErr error,
) {
	if callbackErr != nil {
		callState.recordError(callbackErr)
	}
	if len(values) != nresults {
		if callbackErr == nil {
			callState.recordError(fmt.Errorf("async component callback returned %d results, expected %d", len(values), nresults))
		}
		values = make([]interface{}, nresults)
	}
	if nresults == 0 {
		return
	}

	var resultType C.wasmtime_component_valtype_t
	if !bool(C.wasmtime_component_func_type_result(funcType, &resultType)) {
		callState.recordError(fmt.Errorf("could not retrieve async component callback result type"))
		return
	}
	defer C.wasmtime_component_valtype_delete(&resultType)
	result := C.go_component_val_array_nth(results, 0)
	value := values[0]
	if callbackErr != nil || value == nil {
		var defaultErr error
		if callbackErr != nil {
			value, defaultErr = componentCallbackFailureValue(&resultType, callbackErr)
		} else {
			value, defaultErr = componentDefaultValue(&resultType)
		}
		if defaultErr != nil {
			callState.recordError(defaultErr)
			return
		}
	}
	if marshalErr := componentMarshalArg(value, &resultType, result); marshalErr != nil {
		C.wasmtime_component_val_delete(result)
		callState.recordError(fmt.Errorf("async component callback result: %w", marshalErr))
		fallback, defaultErr := componentDefaultValue(&resultType)
		if defaultErr == nil {
			if fallbackErr := componentMarshalArg(fallback, &resultType, result); fallbackErr != nil {
				callState.recordError(fallbackErr)
			}
		} else {
			callState.recordError(defaultErr)
		}
	}
}

//export goComponentAsyncWorker
func goComponentAsyncWorker(state *C.go_component_async_state_t) {
	contextKey := uintptr(C.go_component_async_state_context_key(state))
	callState := activeComponentCallKey(contextKey)
	if callState == nil {
		callState = &componentCallState{ctx: context.Background()}
	}
	callbackContext, cancel := context.WithCancel(callState.ctx)
	stopCancellationWatch := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCancellationWatch:
				return
			case <-ticker.C:
				if bool(C.go_component_async_state_cancelled(state)) {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(stopCancellationWatch)
		cancel()
	}()

	callback := cgo.Handle(C.go_component_async_state_callback(state)).Value().(ComponentAsyncFunc)
	args := C.go_component_async_state_args(state)
	nargs := int(C.go_component_async_state_nargs(state))
	goArgs := make([]interface{}, nargs)
	for index := range goArgs {
		value, err := componentUnmarshalVal(C.go_component_val_array_nth(args, C.size_t(index)))
		if err != nil {
			writeComponentAsyncResults(
				callState,
				(*C.wasmtime_component_func_type_t)(unsafe.Pointer(C.go_component_async_state_func_type(state))),
				C.go_component_async_state_results(state),
				int(C.go_component_async_state_nresults(state)),
				nil,
				fmt.Errorf("async component callback argument %d: %w", index, err),
			)
			return
		}
		goArgs[index] = value
	}

	var values []interface{}
	var callbackErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				callbackErr = fmt.Errorf("%w: %v", ErrComponentHostPanic, recovered)
			}
		}()
		values, callbackErr = callback(callbackContext, goArgs)
	}()
	writeComponentAsyncResults(
		callState,
		(*C.wasmtime_component_func_type_t)(unsafe.Pointer(C.go_component_async_state_func_type(state))),
		C.go_component_async_state_results(state),
		int(C.go_component_async_state_nresults(state)),
		values,
		callbackErr,
	)
}

//export goFinalizeComponentAsyncFunc
func goFinalizeComponentAsyncFunc(env unsafe.Pointer) {
	cgo.Handle(uintptr(env)).Delete()
}

// ComponentLinkerInstance defines names within a component linker namespace.
type ComponentLinkerInstance struct {
	_ptr *C.wasmtime_component_linker_instance_t
}

func mkComponentLinkerInstance(ptr *C.wasmtime_component_linker_instance_t) *ComponentLinkerInstance {
	instance := &ComponentLinkerInstance{_ptr: ptr}
	runtime.SetFinalizer(instance, func(instance *ComponentLinkerInstance) { instance.Close() })
	return instance
}

func (instance *ComponentLinkerInstance) ptr() *C.wasmtime_component_linker_instance_t {
	if instance._ptr == nil {
		panic("object has been closed already")
	}
	return instance._ptr
}

// Root acquires the linker's root namespace. Close the returned instance before
// using the linker again.
func (linker *ComponentLinker) Root() *ComponentLinkerInstance {
	instance := mkComponentLinkerInstance(C.wasmtime_component_linker_root(linker.ptr()))
	runtime.KeepAlive(linker)
	return instance
}

// AddInstance defines and acquires a nested linker namespace. Close the child
// before using its parent again.
func (instance *ComponentLinkerInstance) AddInstance(name string) (*ComponentLinkerInstance, error) {
	var child *C.wasmtime_component_linker_instance_t
	err := C.wasmtime_component_linker_instance_add_instance(
		instance.ptr(), C._GoStringPtr(name), C._GoStringLen(name), &child,
	)
	runtime.KeepAlive(instance)
	runtime.KeepAlive(name)
	if err != nil {
		return nil, mkError(err)
	}
	return mkComponentLinkerInstance(child), nil
}

// AddFuncAsync defines an asynchronous host function in this namespace.
func (instance *ComponentLinkerInstance) AddFuncAsync(name string, callback ComponentAsyncFunc) error {
	if callback == nil {
		return fmt.Errorf("async component callback is nil")
	}
	handle := cgo.NewHandle(callback)
	err := C.go_component_linker_instance_add_func_async(
		instance.ptr(), C._GoStringPtr(name), C._GoStringLen(name), C.size_t(handle),
	)
	runtime.KeepAlive(instance)
	runtime.KeepAlive(name)
	if err != nil {
		handle.Delete()
		return mkError(err)
	}
	return nil
}

// Close releases this linker namespace and restores access to its parent.
func (instance *ComponentLinkerInstance) Close() {
	if instance._ptr == nil {
		return
	}
	runtime.SetFinalizer(instance, nil)
	C.wasmtime_component_linker_instance_delete(instance._ptr)
	instance._ptr = nil
}

// InstantiateAsync instantiates a component using an async Store.
func (linker *ComponentLinker) InstantiateAsync(ctx context.Context, store Storelike, component *Component) (*ComponentInstance, error) {
	storeContext := store.Context()
	callState, err := beginComponentCall(storeContext, ctx)
	if err != nil {
		return nil, err
	}
	defer endComponentCall(storeContext, callState)

	instanceSlot := C.go_component_instance_slot_new()
	if instanceSlot == nil {
		return nil, fmt.Errorf("could not allocate component instance result")
	}
	defer C.go_component_instance_slot_delete(instanceSlot)
	errorSlot := C.go_component_error_slot_new()
	if errorSlot == nil {
		return nil, fmt.Errorf("could not allocate component error result")
	}
	defer C.go_component_error_slot_delete(errorSlot)

	future := C.wasmtime_component_linker_instantiate_async(
		linker.ptr(), storeContext, component.ptr(), instanceSlot, errorSlot,
	)
	if future == nil {
		if wasmtimeErr := C.go_component_error_slot_get(errorSlot); wasmtimeErr != nil {
			return nil, mkError(wasmtimeErr)
		}
		return nil, fmt.Errorf("could not create component instantiation future")
	}
	defer C.wasmtime_call_future_delete(future)
	if err := pollComponentFuture(ctx, future); err != nil {
		return nil, err
	}
	runtime.KeepAlive(linker)
	runtime.KeepAlive(store)
	runtime.KeepAlive(component)
	if wasmtimeErr := C.go_component_error_slot_get(errorSlot); wasmtimeErr != nil {
		return nil, mkError(wasmtimeErr)
	}
	if callbackErr := callState.resultError(); callbackErr != nil {
		return nil, callbackErr
	}
	return mkComponentInstance(*instanceSlot), nil
}

// CallAsync invokes a component function using an async Store.
func (function *ComponentFunc) CallAsync(ctx context.Context, store Storelike, args ...interface{}) (interface{}, error) {
	storeContext := store.Context()
	callState, err := beginComponentCall(storeContext, ctx)
	if err != nil {
		return nil, err
	}
	defer endComponentCall(storeContext, callState)

	funcType := C.wasmtime_component_func_type(&function.val, storeContext)
	if funcType == nil {
		return nil, fmt.Errorf("could not retrieve component function type")
	}
	defer C.wasmtime_component_func_type_delete(funcType)
	paramCount := int(C.wasmtime_component_func_type_param_count(funcType))
	if len(args) != paramCount {
		return nil, fmt.Errorf("wrong number of arguments: got %d, expected %d", len(args), paramCount)
	}
	cArgs := C.go_component_val_array_new(C.size_t(paramCount))
	defer C.go_component_val_array_delete(cArgs, C.size_t(paramCount))
	for index := 0; index < paramCount; index++ {
		var name *C.char
		var nameLen C.size_t
		var paramType C.wasmtime_component_valtype_t
		if !bool(C.wasmtime_component_func_type_param_nth(funcType, C.size_t(index), &name, &nameLen, &paramType)) {
			return nil, fmt.Errorf("could not retrieve parameter %d", index)
		}
		marshalErr := componentMarshalArg(args[index], &paramType, C.go_component_val_array_nth(cArgs, C.size_t(index)))
		C.wasmtime_component_valtype_delete(&paramType)
		if marshalErr != nil {
			return nil, fmt.Errorf("argument %d: %w", index, marshalErr)
		}
	}

	var resultType C.wasmtime_component_valtype_t
	hasResult := bool(C.wasmtime_component_func_type_result(funcType, &resultType))
	if hasResult {
		defer C.wasmtime_component_valtype_delete(&resultType)
	}
	resultCount := 0
	if hasResult {
		resultCount = 1
	}
	cResults := C.go_component_val_array_new(C.size_t(resultCount))
	defer C.go_component_val_array_delete(cResults, C.size_t(resultCount))
	errorSlot := C.go_component_error_slot_new()
	if errorSlot == nil {
		return nil, fmt.Errorf("could not allocate component error result")
	}
	defer C.go_component_error_slot_delete(errorSlot)
	funcCopy := C.go_component_func_copy(&function.val)
	if funcCopy == nil {
		return nil, fmt.Errorf("could not allocate component function handle")
	}
	defer C.go_component_func_copy_delete(funcCopy)

	future := C.go_component_func_call_concurrent_async(
		funcCopy, storeContext, cArgs, C.size_t(paramCount), cResults, C.size_t(resultCount), errorSlot,
	)
	if future == nil {
		if wasmtimeErr := C.go_component_error_slot_get(errorSlot); wasmtimeErr != nil {
			return nil, mkError(wasmtimeErr)
		}
		return nil, fmt.Errorf("could not create component call future")
	}
	defer C.wasmtime_call_future_delete(future)
	if err := pollComponentFuture(ctx, future); err != nil {
		return nil, err
	}
	runtime.KeepAlive(function)
	runtime.KeepAlive(store)
	if wasmtimeErr := C.go_component_error_slot_get(errorSlot); wasmtimeErr != nil {
		return nil, mkError(wasmtimeErr)
	}
	if callbackErr := callState.resultError(); callbackErr != nil {
		return nil, callbackErr
	}
	if !hasResult {
		return nil, nil
	}
	return componentUnmarshalVal(C.go_component_val_array_nth(cResults, 0))
}

func pollComponentFuture(ctx context.Context, future *C.wasmtime_call_future_t) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pollInterval := time.NewTicker(100 * time.Microsecond)
	defer pollInterval.Stop()
	for {
		if bool(C.wasmtime_call_future_poll(future)) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pollInterval.C:
		}
	}
}

func componentCallbackFailureValue(ty *C.wasmtime_component_valtype_t, callbackErr error) (interface{}, error) {
	if ty.kind != C.WASMTIME_COMPONENT_VALTYPE_RESULT {
		return componentDefaultValue(ty)
	}
	resultType := C.go_async_valtype_result(ty)
	var errorType C.wasmtime_component_valtype_t
	if !bool(C.wasmtime_component_result_type_err(resultType, &errorType)) {
		return ComponentResult{OK: false}, nil
	}
	defer C.wasmtime_component_valtype_delete(&errorType)
	if errorType.kind == C.WASMTIME_COMPONENT_VALTYPE_STRING {
		return ComponentResult{OK: false, Value: callbackErr.Error()}, nil
	}
	value, err := componentDefaultValue(&errorType)
	if err != nil {
		return nil, fmt.Errorf("result error payload: %w", err)
	}
	return ComponentResult{OK: false, Value: value}, nil
}
