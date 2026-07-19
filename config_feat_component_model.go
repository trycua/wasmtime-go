package wasmtime

// #include <wasmtime.h>
import "C"
import "runtime"

// SetWasmComponentModel configures whether the wasm component model proposal is
// enabled.
func (cfg *Config) SetWasmComponentModel(enabled bool) {
	C.wasmtime_config_wasm_component_model_set(cfg.ptr(), C.bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetConcurrencySupport configures whether support for concurrent execution of
// WebAssembly is enabled for stores using this configuration.
func (cfg *Config) SetConcurrencySupport(enabled bool) {
	C.wasmtime_config_concurrency_support_set(cfg.ptr(), C.bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetWasmComponentModelAsync configures support for asynchronous component
// functions and host imports.
func (cfg *Config) SetWasmComponentModelAsync(enabled bool) {
	C.wasmtime_config_wasm_component_model_async_set(cfg.ptr(), C.bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetWasmComponentModelMoreAsyncBuiltins configures the component model's
// additional async built-in intrinsics.
func (cfg *Config) SetWasmComponentModelMoreAsyncBuiltins(enabled bool) {
	C.wasmtime_config_wasm_component_model_more_async_builtins_set(cfg.ptr(), C.bool(enabled))
	runtime.KeepAlive(cfg)
}

// SetWasmComponentModelAsyncStackful configures stackful async component
// execution.
func (cfg *Config) SetWasmComponentModelAsyncStackful(enabled bool) {
	C.wasmtime_config_wasm_component_model_async_stackful_set(cfg.ptr(), C.bool(enabled))
	runtime.KeepAlive(cfg)
}
