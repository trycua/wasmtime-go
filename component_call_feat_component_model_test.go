package wasmtime

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

const primitiveTestComponent = `
(component
  (core module $m
    (func (export "id_i32") (param i32) (result i32) local.get 0)
    (func (export "id_i64") (param i64) (result i64) local.get 0)
    (func (export "id-f32") (param f32) (result f32) local.get 0)
    (func (export "id-f64") (param f64) (result f64) local.get 0)
    (func (export "first_i32") (param i32 i32) (result i32) local.get 0)
    (func (export "noop")))
  (core instance $i (instantiate $m))
  (func (export "noop") (canon lift (core func $i "noop")))
  (func (export "id-bool") (param "x" bool) (result bool) (canon lift (core func $i "id_i32")))
  (func (export "id-s8")   (param "x" s8)   (result s8)   (canon lift (core func $i "id_i32")))
  (func (export "id-u8")   (param "x" u8)   (result u8)   (canon lift (core func $i "id_i32")))
  (func (export "id-s16")  (param "x" s16)  (result s16)  (canon lift (core func $i "id_i32")))
  (func (export "id-u16")  (param "x" u16)  (result u16)  (canon lift (core func $i "id_i32")))
  (func (export "id-s32")  (param "x" s32)  (result s32)  (canon lift (core func $i "id_i32")))
  (func (export "id-u32")  (param "x" u32)  (result u32)  (canon lift (core func $i "id_i32")))
  (func (export "id-s64")  (param "x" s64)  (result s64)  (canon lift (core func $i "id_i64")))
  (func (export "id-u64")  (param "x" u64)  (result u64)  (canon lift (core func $i "id_i64")))
  (func (export "id-f32")  (param "x" f32)  (result f32)  (canon lift (core func $i "id-f32")))
  (func (export "id-f64")  (param "x" f64)  (result f64)  (canon lift (core func $i "id-f64")))
  (func (export "id-char") (param "c" char) (result char) (canon lift (core func $i "id_i32")))
  (func (export "first-s32") (param "a" s32) (param "b" s32) (result s32) (canon lift (core func $i "first_i32"))))
`

func setupPrimitiveTest(t *testing.T) (*Store, *ComponentInstance) {
	t.Helper()
	engine := newComponentEngine()
	store := NewStore(engine)

	wasm, err := Wat2Wasm(primitiveTestComponent)
	require.NoError(t, err)

	component, err := NewComponent(engine, wasm)
	require.NoError(t, err)
	t.Cleanup(component.Close)

	linker := NewComponentLinker(engine)
	t.Cleanup(linker.Close)

	instance, err := linker.Instantiate(store, component)
	require.NoError(t, err)

	return store, instance
}

func TestComponentInstanceGetFunc(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	cases := []struct {
		name      string
		target    string
		wantFound bool
	}{
		{"existing", "id-s32", true},
		{"missing", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := instance.GetFunc(store, tc.target)
			if tc.wantFound {
				require.NotNil(t, f)
			} else {
				require.Nil(t, f)
			}
		})
	}
}

func TestComponentCallVoid(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "noop")
	require.NotNil(t, f)
	got, err := f.Call(store)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestComponentCallBool(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "id-bool")
	require.NotNil(t, f)
	for _, want := range []bool{true, false} {
		t.Run(fmt.Sprintf("%v", want), func(t *testing.T) {
			got, err := f.Call(store, want)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestComponentCallSignedIntegers(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	cases := []struct {
		name string
		val  interface{}
	}{
		{"id-s8", int8(-128)},
		{"id-s8", int8(127)},
		{"id-s16", int16(-32768)},
		{"id-s16", int16(32767)},
		{"id-s32", int32(-2147483648)},
		{"id-s32", int32(2147483647)},
		{"id-s64", int64(-9223372036854775808)},
		{"id-s64", int64(9223372036854775807)},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%v", tc.name, tc.val), func(t *testing.T) {
			f := instance.GetFunc(store, tc.name)
			require.NotNil(t, f)
			got, err := f.Call(store, tc.val)
			require.NoError(t, err)
			require.Equal(t, tc.val, got)
		})
	}
}

func TestComponentCallUnsignedIntegers(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	cases := []struct {
		name string
		val  interface{}
	}{
		{"id-u8", uint8(0)},
		{"id-u8", uint8(255)},
		{"id-u16", uint16(0)},
		{"id-u16", uint16(65535)},
		{"id-u32", uint32(0)},
		{"id-u32", uint32(4294967295)},
		{"id-u64", uint64(0)},
		{"id-u64", uint64(18446744073709551615)},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%v", tc.name, tc.val), func(t *testing.T) {
			f := instance.GetFunc(store, tc.name)
			require.NotNil(t, f)
			got, err := f.Call(store, tc.val)
			require.NoError(t, err)
			require.Equal(t, tc.val, got)
		})
	}
}

func TestComponentCallFloats(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	cases := []struct {
		name string
		val  interface{}
	}{
		{"id-f32", float32(3.14)},
		{"id-f64", float64(2.718281828)},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%v", tc.name, tc.val), func(t *testing.T) {
			f := instance.GetFunc(store, tc.name)
			require.NotNil(t, f)
			got, err := f.Call(store, tc.val)
			require.NoError(t, err)
			require.Equal(t, tc.val, got)
		})
	}
}

func TestComponentCallChar(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "id-char")
	require.NotNil(t, f)
	cases := []struct {
		name string
		val  rune
	}{
		{"ascii", 'A'},
		{"hiragana", 'あ'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.Call(store, tc.val)
			require.NoError(t, err)
			require.Equal(t, tc.val, got)
		})
	}
}

func TestComponentCallMultipleParams(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "first-s32")
	require.NotNil(t, f)
	// `first-s32` is wired to a core function that returns its first argument,
	// so this verifies that multiple arguments are marshaled in the right
	// order.
	got, err := f.Call(store, int32(7), int32(35))
	require.NoError(t, err)
	require.Equal(t, int32(7), got)
}

func TestComponentCallWrongArgCount(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "id-s32")
	require.NotNil(t, f)
	cases := []struct {
		name string
		args []interface{}
	}{
		{"too_few", nil},
		{"too_many", []interface{}{int32(1), int32(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Call(store, tc.args...)
			require.Error(t, err)
		})
	}
}

func TestComponentCallWrongArgType(t *testing.T) {
	store, instance := setupPrimitiveTest(t)
	f := instance.GetFunc(store, "id-s32")
	require.NotNil(t, f)
	_, err := f.Call(store, int64(1)) // expects int32
	require.Error(t, err)
}

// stringTestComponent exposes a `() -> string` (returns "hello") and a
// `(string) -> string` identity, exercising the canonical-ABI plumbing for
// strings. Memory layout used by the core module:
//
//	[0..5]      = "hello" data segment
//	[1024..]    = bump allocator pool (cabi_realloc returns a fresh chunk
//	              here per call, advancing the bump pointer at memory[8])
//	[16..24]    = scratch return area for string returns (ptr at +0, len at +4)
//
// The canon lift convention expects the core function to return a single i32
// pointing to a (ptr, len) struct in linear memory.
const stringTestComponent = `
(component
  (core module $m
    (memory (export "memory") 1)
    (data (i32.const 0) "hello")
    ;; cabi_realloc: bump allocator. The bump cursor lives at memory[8].
    (func (export "cabi_realloc")
      (param $orig_ptr i32) (param $orig_size i32) (param $align i32) (param $new_size i32)
      (result i32)
      (local $cursor i32)
      ;; if cursor is 0, initialize it to 1024
      i32.const 8
      i32.load
      local.tee $cursor
      i32.eqz
      if
        i32.const 1024
        local.set $cursor
      end
      ;; advance cursor by new_size and store back
      i32.const 8
      local.get $cursor
      local.get $new_size
      i32.add
      i32.store
      ;; return original cursor as the allocation
      local.get $cursor)
    ;; ret_hello returns a pointer to a (ptr=0, len=5) struct at memory[16..24].
    (func (export "ret_hello") (result i32)
      i32.const 16 i32.const 0 i32.store
      i32.const 20 i32.const 5 i32.store
      i32.const 16)
    ;; id_string_core writes (ptr_in, len_in) to memory[16..24] and returns 16.
    (func (export "id_string_core")
      (param $ptr_in i32) (param $len_in i32) (result i32)
      i32.const 16 local.get $ptr_in i32.store
      i32.const 20 local.get $len_in i32.store
      i32.const 16))
  (core instance $i (instantiate $m))
  (func (export "ret-hello") (result string)
    (canon lift
      (core func $i "ret_hello")
      (memory $i "memory")
      (realloc (func $i "cabi_realloc"))
      string-encoding=utf8))
  (func (export "id-string") (param "s" string) (result string)
    (canon lift
      (core func $i "id_string_core")
      (memory $i "memory")
      (realloc (func $i "cabi_realloc"))
      string-encoding=utf8)))
`

func setupStringTest(t *testing.T) (*Store, *ComponentInstance) {
	t.Helper()
	engine := newComponentEngine()
	store := NewStore(engine)

	wasm, err := Wat2Wasm(stringTestComponent)
	require.NoError(t, err)

	component, err := NewComponent(engine, wasm)
	require.NoError(t, err)
	t.Cleanup(component.Close)

	linker := NewComponentLinker(engine)
	t.Cleanup(linker.Close)

	instance, err := linker.Instantiate(store, component)
	require.NoError(t, err)

	return store, instance
}

func TestComponentCallStringReturn(t *testing.T) {
	store, instance := setupStringTest(t)
	f := instance.GetFunc(store, "ret-hello")
	require.NotNil(t, f)
	got, err := f.Call(store)
	require.NoError(t, err)
	require.Equal(t, "hello", got)
}

func TestComponentCallStringRoundtrip(t *testing.T) {
	store, instance := setupStringTest(t)
	f := instance.GetFunc(store, "id-string")
	require.NotNil(t, f)
	cases := []struct {
		name string
		val  string
	}{
		{"empty", ""},
		{"ascii", "ascii"},
		{"japanese", "日本語"},
		{"emoji", "emoji 🎉"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.Call(store, tc.val)
			require.NoError(t, err)
			require.Equal(t, tc.val, got)
		})
	}
}

const sumTypeTestComponent = `
(component
  (type $e' (enum "a" "b"))
  (export $e "e" (type $e'))
  (type $o' (option string))
  (export $o "o" (type $o'))
  (type $r' (result string (error string)))
  (export $r "r" (type $r'))
  (type $v' (variant (case "none") (case "text" string)))
  (export $v "v" (type $v'))
  (core module $m
    (func (export "id-i32") (param i32) (result i32) local.get 0)
    (func (export "id-wide") (param i32 i32 i32) (result i32)
      (local $ret i32)
      i32.const 2048
      local.set $ret
      local.get $ret
      local.get 0
      i32.store
      local.get $ret
      local.get 1
      i32.store offset=4
      local.get $ret
      local.get 2
      i32.store offset=8
      local.get $ret)
    (memory (export "memory") 1)
    (global $next (mut i32) (i32.const 1024))
    (func (export "cabi_realloc")
      (param i32 i32 i32 i32) (result i32)
      (local $ret i32)
      global.get $next
      local.tee $ret
      local.get 3
      i32.add
      global.set $next
      local.get $ret))
  (core instance $i (instantiate $m))
  (func (export "id-enum") (param "x" $e) (result $e)
    (canon lift (core func $i "id-i32")))
  (func (export "id-option") (param "x" $o) (result $o)
    (canon lift (core func $i "id-wide") (memory $i "memory") (realloc (func $i "cabi_realloc"))))
  (func (export "id-result") (param "x" $r) (result $r)
    (canon lift (core func $i "id-wide") (memory $i "memory") (realloc (func $i "cabi_realloc"))))
  (func (export "id-variant") (param "x" $v) (result $v)
    (canon lift (core func $i "id-wide") (memory $i "memory") (realloc (func $i "cabi_realloc")))))
`

func setupSumTypeTest(t *testing.T) (*Store, *ComponentInstance) {
	t.Helper()
	engine := newComponentEngine()
	store := NewStore(engine)
	wasm, err := Wat2Wasm(sumTypeTestComponent)
	require.NoError(t, err)
	component, err := NewComponent(engine, wasm)
	require.NoError(t, err)
	t.Cleanup(component.Close)
	linker := NewComponentLinker(engine)
	t.Cleanup(linker.Close)
	instance, err := linker.Instantiate(store, component)
	require.NoError(t, err)
	return store, instance
}

func TestComponentCallEnum(t *testing.T) {
	store, instance := setupSumTypeTest(t)
	got, err := instance.GetFunc(store, "id-enum").Call(store, ComponentEnum("b"))
	require.NoError(t, err)
	require.Equal(t, ComponentEnum("b"), got)
}

func TestComponentCallOption(t *testing.T) {
	store, instance := setupSumTypeTest(t)
	for _, want := range []ComponentOption{{}, {Some: true, Value: "hello"}} {
		got, err := instance.GetFunc(store, "id-option").Call(store, want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestComponentCallResult(t *testing.T) {
	store, instance := setupSumTypeTest(t)
	for _, want := range []ComponentResult{{OK: true, Value: "ok"}, {OK: false, Value: "error"}} {
		got, err := instance.GetFunc(store, "id-result").Call(store, want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestComponentCallVariant(t *testing.T) {
	store, instance := setupSumTypeTest(t)
	for _, want := range []ComponentVariant{{Case: "none"}, {Case: "text", Value: "hello"}} {
		got, err := instance.GetFunc(store, "id-variant").Call(store, want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}
