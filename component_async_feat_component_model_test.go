//go:build linux && amd64

package wasmtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

const asyncHostTestComponent = `
(component
  (import "host" (instance $host
    (export "add" (func async (param "x" u32) (param "y" u32) (result u32)))))
  (core module $m
    (func (export "run") (param i32 i32) (result i32)
      local.get 0
      local.get 1
      i32.add))
  (core instance $i (instantiate $m))
  (func (export "run") async (param "x" u32) (param "y" u32) (result u32)
    (canon lift (core func $i "run"))))
`

func setupAsyncHostTest(t *testing.T) (*Store, *ComponentInstance) {
	t.Helper()
	config := NewConfig()
	config.SetWasmComponentModel(true)
	config.SetWasmComponentModelAsync(true)
	config.SetWasmComponentModelAsyncStackful(true)
	config.SetConcurrencySupport(true)
	engine := NewEngineWithConfig(config)
	t.Cleanup(engine.Close)
	store := NewStore(engine)
	t.Cleanup(store.Close)
	wasm, err := Wat2Wasm(asyncHostTestComponent)
	require.NoError(t, err)
	component, err := NewComponent(engine, wasm)
	require.NoError(t, err)
	t.Cleanup(component.Close)
	linker := NewComponentLinker(engine)
	t.Cleanup(linker.Close)
	root := linker.Root()
	host, err := root.AddInstance("host")
	require.NoError(t, err)
	require.NoError(t, host.AddFuncAsync("add", func(context.Context, []interface{}) ([]interface{}, error) {
		return []interface{}{uint32(0)}, nil
	}))
	host.Close()
	root.Close()
	instance, err := linker.InstantiateAsync(context.Background(), store, component)
	require.NoError(t, err)
	return store, instance
}

func TestComponentConcurrentHostDefinitionAndCall(t *testing.T) {
	store, instance := setupAsyncHostTest(t)
	got, err := instance.GetFunc(store, "run").CallAsync(context.Background(), store, uint32(34), uint32(35))
	require.NoError(t, err)
	require.Equal(t, uint32(69), got)
}

func TestComponentAsyncRejectsOverlappingCallsOnOneStore(t *testing.T) {
	store, _ := setupAsyncHostTest(t)
	state, err := beginComponentCall(store.Context(), context.Background())
	require.NoError(t, err)
	defer endComponentCall(store.Context(), state)
	_, err = beginComponentCall(store.Context(), context.Background())
	require.ErrorContains(t, err, "already active")
}

func TestComponentAsyncAllowsIndependentStores(t *testing.T) {
	storeA, _ := setupAsyncHostTest(t)
	storeB, _ := setupAsyncHostTest(t)
	stateA, err := beginComponentCall(storeA.Context(), context.Background())
	require.NoError(t, err)
	defer endComponentCall(storeA.Context(), stateA)
	stateB, err := beginComponentCall(storeB.Context(), context.Background())
	require.NoError(t, err)
	defer endComponentCall(storeB.Context(), stateB)
}
