// SPDX-License-Identifier: MIT

package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Embedded is exported: RegisterFlags takes the address of an embedded struct.
type Embedded struct {
	E string `flag:"e" desc:"embedded"`
}

type inner struct {
	Host string `env:"T_HOST"`
}

type sample struct {
	inner
	S      string   `env:"T_S"`
	U8     uint8    `env:"T_U8"`
	I16    int16    `env:"T_I16"`
	F      float64  `env:"T_F"`
	B      bool     `env:"T_B"`
	L      []string `env:"T_L"`
	Ints   []int    `env:"T_INTS"`
	Other  struct{} `env:"T_OTHER"`
	Untag  string
	hidden string `env:"T_HIDDEN"`
}

func TestLoadFromEnv(t *testing.T) {
	for k, v := range map[string]string{
		"T_HOST": "h", "T_S": "s", "T_U8": "200", "T_I16": "-300", "T_F": "1.5", "T_B": "true",
		"T_L": "a, b ,c", "T_INTS": "1,2", "T_OTHER": "x", "T_HIDDEN": "x",
	} {
		t.Setenv(k, v)
	}
	var c sample
	if err := LoadFromEnv(&c); err != nil {
		t.Fatal(err)
	}
	if c.hidden != "" {
		t.Errorf("an unexported field was set: %q", c.hidden)
	}
	want := sample{inner: inner{Host: "h"}, S: "s", U8: 200, I16: -300, F: 1.5, B: true, L: []string{"a", "b", "c"}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("%+v, want %+v", c, want)
	}
}

func TestLoadFromEnvReportsEveryBadValue(t *testing.T) {
	for k, v := range map[string]string{"T_HOST": "", "T_U8": "256", "T_I16": "40000", "T_F": "x", "T_B": "perhaps"} {
		t.Setenv(k, v)
	}
	err := LoadFromEnv(&sample{})
	if err == nil {
		t.Fatal("no error")
	}
	for _, name := range []string{"T_U8", "T_I16", "T_F", "T_B"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%v does not name %s", err, name)
		}
	}
	t.Setenv("T_U8", "x")
	t.Setenv("T_I16", "x")
	if err := LoadFromEnv(&sample{}); err == nil || !strings.Contains(err.Error(), `T_U8="x"`) || !strings.Contains(err.Error(), `T_I16="x"`) {
		t.Errorf("not numbers: %v", err)
	}
}

func TestLoadFromEnvIgnoresWhatIsNotAStructPointer(t *testing.T) {
	var n int
	for _, v := range []any{n, &n, sample{}} {
		if err := LoadFromEnv(v); err != nil {
			t.Errorf("%T: %v", v, err)
		}
	}
}

func TestRegisterFlagsEveryKind(t *testing.T) {
	var c struct {
		Embedded
		S    string   `flag:"s" env:"X_S" desc:"string"`
		U8   uint8    `flag:"u8"`
		U16  uint16   `flag:"u16"`
		U32  uint32   `flag:"u32"`
		U64  uint64   `flag:"u64"`
		U    uint     `flag:"u"`
		I    int      `flag:"i"`
		I8   int8     `flag:"i8"`
		I16  int16    `flag:"i16"`
		I32  int32    `flag:"i32"`
		I64  int64    `flag:"i64"`
		F    float64  `flag:"f"`
		B    bool     `flag:"b"`
		L    []string `flag:"l"`
		L2   []string `flag:"l2"`
		Skip string   `flag:"-"`
		None string
	}
	c.L2 = []string{"kept"}
	cmd := &cobra.Command{Use: "x"}
	RegisterFlags(cmd, &c)
	if err := cmd.ParseFlags([]string{"--e=x", "--s=y", "--u8=1", "--u16=2", "--u32=3", "--u64=4", "--u=5", "--i=-6", "--i8=-7",
		"--i16=-8", "--i32=-9", "--i64=-10", "--f=1.5", "--b", "--l=a,b"}); err != nil {
		t.Fatal(err)
	}
	if c.E != "x" || c.S != "y" || c.U8 != 1 || c.U16 != 2 || c.U32 != 3 || c.U64 != 4 || c.U != 5 || c.I != -6 || c.I8 != -7 ||
		c.I16 != -8 || c.I32 != -9 || c.I64 != -10 || c.F != 1.5 || !c.B || !reflect.DeepEqual(c.L, []string{"a", "b"}) ||
		!reflect.DeepEqual(c.L2, []string{"kept"}) {
		t.Fatalf("%+v", c)
	}
	if got := cmd.Flags().Lookup("s").Usage; got != "string [env: X_S]" {
		t.Errorf("usage %q", got)
	}
	if cmd.Flags().Lookup("-") != nil {
		t.Error(`flag:"-" registered a flag`)
	}
}

func TestRegisterFlagsRefusesWhatItCannotBind(t *testing.T) {
	var ints struct {
		V []int `flag:"v"`
	}
	var kind struct {
		V map[string]string `flag:"v"`
	}
	for name, cfg := range map[string]any{"slice": &ints, "kind": &kind} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			RegisterFlags(&cobra.Command{Use: "x"}, cfg)
		}()
	}
}
