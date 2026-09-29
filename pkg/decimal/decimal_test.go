package decimal

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]string{
		"12.50": "12.50", "+3": "3", "-0.5": "-0.5", "007.10": "7.10", "-0.00": "0.00",
		"1.25e3": "1250", "125e-4": "0.0125", "5E-1": "0.5", "0": "0",
	} {
		got, err := Parse(in)
		if err != nil || string(got) != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-", "1.", ".5", "1,5", "abc", "NaN", "1e", "1e99999", "0x10", "1/2"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
		if bad != "" && Decimal(bad).Valid() {
			t.Errorf("Valid(%q)", bad)
		}
	}
}

func TestCmpFitsFromRat(t *testing.T) {
	// 0.1 + 0.2 is exactly 0.3, unlike float64.
	sum := new(big.Rat).Add(Decimal("0.1").Rat(), Decimal("0.2").Rat())
	if got := FromRat(sum, 2); got != "0.30" || got.Cmp("0.3") != 0 {
		t.Fatalf("0.1+0.2 = %s", got)
	}
	if Decimal("12.50").Cmp("12.5") != 0 || Decimal("-1").Cmp("0.01") != -1 || Decimal("100000000000000000000.01").Cmp("100000000000000000000") != 1 {
		t.Fatal("Cmp")
	}
	if Decimal("").Sign() != 0 || Decimal("").String() != "0" {
		t.Fatal("zero value")
	}
	for in, want := range map[string]string{"1/8": "0.13", "-1/8": "-0.13", "1/3": "0.33", "-2/3": "-0.67", "5": "5.00"} {
		r, _ := new(big.Rat).SetString(in)
		if got := FromRat(r, 2); string(got) != want {
			t.Errorf("FromRat(%s) = %s, want %s", in, got, want)
		}
	}
	for d, want := range map[Decimal]bool{"9999999999.99": true, "99999999999": false, "1.005": false, "1.500": true, "-0.01": true, "x": false, "0.001e1": true} {
		if d.Fits(12, 2) != want {
			t.Errorf("Fits(%s, 12, 2) != %v", d, want)
		}
	}
}

func TestJSON(t *testing.T) {
	var v struct {
		A Decimal  `json:"a"`
		B *Decimal `json:"b"`
		C Decimal  `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a":"12.50","b":0.1,"c":1e2}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != "12.50" || v.B == nil || *v.B != "0.1" || v.C != "100" {
		t.Fatalf("%+v", v)
	}
	out, _ := json.Marshal(v)
	if string(out) != `{"a":"12.50","b":"0.1","c":"100"}` {
		t.Fatalf("%s", out)
	}
	if err := json.Unmarshal([]byte(`{"a":"twelve"}`), &v); err == nil {
		t.Fatal("accepted a non-number")
	}
}

// TestPgx: pgx encodes and decodes numeric through Decimal without a
// float in between, keeping the scale.
func TestPgx(t *testing.T) {
	m := pgtype.NewMap()
	for _, format := range []int16{pgtype.BinaryFormatCode, pgtype.TextFormatCode} {
		for _, in := range []Decimal{"12.50", "-0.0001", "123456789012345678901234567890.123456789", "0"} {
			buf, err := m.Encode(pgtype.NumericOID, format, in, nil)
			if err != nil {
				t.Fatal(err)
			}
			var out Decimal
			if err := m.Scan(pgtype.NumericOID, format, buf, &out); err != nil {
				t.Fatal(err)
			}
			if out != in {
				t.Errorf("format %d: %s came back as %s", format, in, out)
			}
			var p *Decimal
			if err := m.Scan(pgtype.NumericOID, format, nil, &p); err != nil || p != nil {
				t.Errorf("NULL into *Decimal: %v %v", p, err)
			}
		}
	}
}
