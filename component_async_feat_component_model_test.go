package wasmtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const asyncHostTestComponent = `
(component
  (import "host" (instance $host
    (export "add" (func (param "x" u32) (param "y" u32) (result u32)))))
  (core module $m
    (import "host" "add" (func $add (param i32 i32) (result i32)))
    (func (export "run") (param i32 i32) (result i32)
      local.get 0
      local.get 1
      call $add))
  (core func $add-lowered (canon lower (func $host "add")))
  (core instance $i (instantiate $m
    (with "host" (instance (export "add" (func $add-lowered))))))
  (func (export "run") (param "x" u32) (param "y" u32) (result u32)
    (canon lift (core func $i "run"))))
`

func setupAsyncHostTest(t *testing.T, callback ComponentAsyncFunc) (*Store, *ComponentInstance) {
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
	require.NoError(t, host.AddFuncAsync("add", callback))
	host.Close()
	root.Close()
	instance, err := linker.InstantiateAsync(context.Background(), store, component)
	require.NoError(t, err)
	return store, instance
}

func TestComponentAsyncHostCall(t *testing.T) {
	store, instance := setupAsyncHostTest(t, func(ctx context.Context, args []interface{}) ([]interface{}, error) {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []interface{}{args[0].(uint32) + args[1].(uint32)}, nil
	})
	got, err := instance.GetFunc(store, "run").CallAsync(context.Background(), store, uint32(34), uint32(35))
	require.NoError(t, err)
	require.Equal(t, uint32(69), got)
}

func TestComponentAsyncHostCancellation(t *testing.T) {
	store, instance := setupAsyncHostTest(t, func(ctx context.Context, _ []interface{}) ([]interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := instance.GetFunc(store, "run").CallAsync(ctx, store, uint32(1), uint32(2))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestComponentAsyncHostPanicIsContained(t *testing.T) {
	store, instance := setupAsyncHostTest(t, func(context.Context, []interface{}) ([]interface{}, error) {
		panic("boom")
	})
	_, err := instance.GetFunc(store, "run").CallAsync(context.Background(), store, uint32(1), uint32(2))
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrComponentHostPanic))
	require.Contains(t, err.Error(), "boom")
}

func TestComponentAsyncRejectsOverlappingCallsOnOneStore(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	store, instance := setupAsyncHostTest(t, func(ctx context.Context, args []interface{}) ([]interface{}, error) {
		close(started)
		select {
		case <-release:
			return []interface{}{args[0].(uint32) + args[1].(uint32)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	function := instance.GetFunc(store, "run")
	firstDone := make(chan error, 1)
	go func() {
		_, err := function.CallAsync(context.Background(), store, uint32(1), uint32(2))
		firstDone <- err
	}()
	<-started
	_, err := function.CallAsync(context.Background(), store, uint32(3), uint32(4))
	require.ErrorContains(t, err, "already active")
	close(release)
	require.NoError(t, <-firstDone)
}

func TestComponentAsyncAllowsIndependentStores(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	callback := func(ctx context.Context, args []interface{}) ([]interface{}, error) {
		started <- struct{}{}
		select {
		case <-release:
			return []interface{}{args[0].(uint32) + args[1].(uint32)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	storeA, instanceA := setupAsyncHostTest(t, callback)
	storeB, instanceB := setupAsyncHostTest(t, callback)
	errors := make(chan error, 2)
	go func() {
		_, err := instanceA.GetFunc(storeA, "run").CallAsync(context.Background(), storeA, uint32(1), uint32(2))
		errors <- err
	}()
	go func() {
		_, err := instanceB.GetFunc(storeB, "run").CallAsync(context.Background(), storeB, uint32(3), uint32(4))
		errors <- err
	}()
	<-started
	<-started
	close(release)
	require.NoError(t, <-errors)
	require.NoError(t, <-errors)
}
