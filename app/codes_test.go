package main

import (
	"regexp"
	"strings"
	"testing"
)

var (
	reCardCode = regexp.MustCompile(`^LEC[0-9]{5}$`)
	reCopyCode = regexp.MustCompile(`^VOL[0-9]{6}$`)
)

// A prefix, then digits, the last a check digit.
func TestNewCodeShape(t *testing.T) {
	for _, c := range []struct {
		prefix string
		digits int
		re     *regexp.Regexp
	}{{codePrefixCard, cardDigits, reCardCode}, {codePrefixCopy, copyDigits, reCopyCode}} {
		for i := 0; i < 200; i++ {
			code := newCode(c.prefix, c.digits)
			if !c.re.MatchString(code) {
				t.Fatalf("newCode(%s) = %q, want %s", c.prefix, code, c.re)
			}
			if misreadCode(code) {
				t.Fatalf("newCode(%s) = %q, which misreadCode refuses", c.prefix, code)
			}
		}
	}
}

// A small number keeps its leading zeros.
func TestNewCodeKeepsItsWidth(t *testing.T) {
	short := 0
	for i := 0; i < 5000; i++ {
		code := newCode(codePrefixCopy, copyDigits)
		if len(code) != 9 {
			t.Fatalf("%q is %d characters", code, len(code))
		}
		if strings.HasPrefix(code, codePrefixCopy+"0") {
			short++
		}
	}
	// A tenth of the range starts with a zero.
	if short == 0 {
		t.Error("no code below 10000 in 5000 draws — leading zeros are not being kept")
	}
}

// Codes say nothing about order.
func TestCodesCarryNoOrder(t *testing.T) {
	const draws = 500
	ascending := 0
	prev := -1
	seen := make(map[string]bool, draws)
	for i := 0; i < draws; i++ {
		code := newCode(codePrefixCopy, copyDigits)
		seen[code] = true
		n := 0
		for _, c := range code[3:] {
			n = n*10 + int(c-'0')
		}
		if prev >= 0 && n > prev {
			ascending++
		}
		prev = n
	}
	// A sequence would climb every single time. Chance puts this near half.
	if ascending > draws*4/5 {
		t.Errorf("%d of %d draws climbed: the numbers look like a sequence", ascending, draws)
	}
	// 500 draws from a hundred thousand collide a handful of times at most.
	if len(seen) < draws-10 {
		t.Errorf("%d distinct codes out of %d draws", len(seen), draws)
	}
}

// Damm's published example: 572 carries check digit 4.
func TestDammKnownValue(t *testing.T) {
	if got := damm("572"); got != 4 {
		t.Errorf("damm(572) = %d, want 4", got)
	}
	if got := damm("5724"); got != 0 {
		t.Errorf("damm(5724) = %d, want 0", got)
	}
}

// One wrong digit or one adjacent swap never passes for another valid code,
// checked exhaustively over every card code.
func TestCheckDigitCatchesTypos(t *testing.T) {
	for n := 0; n < 10000; n++ {
		body := []byte(strings.Repeat("0", 4))
		for i, v := 3, n; i >= 0; i, v = i-1, v/10 {
			body[i] = byte('0' + v%10)
		}
		code := []byte(codePrefixCard + string(body) + string('0'+damm(string(body))))
		digits := code[len(codePrefixCard):]
		for i := range digits {
			orig := digits[i]
			for d := byte('0'); d <= '9'; d++ {
				if d == orig {
					continue
				}
				digits[i] = d
				if !misreadCode(string(code)) {
					t.Fatalf("%s with digit %d changed passes as valid", code, i)
				}
			}
			digits[i] = orig
			if i+1 < len(digits) && digits[i] != digits[i+1] {
				digits[i], digits[i+1] = digits[i+1], digits[i]
				if !misreadCode(string(code)) {
					t.Fatalf("%s with digits %d and %d swapped passes as valid", code, i, i+1)
				}
				digits[i], digits[i+1] = digits[i+1], digits[i]
			}
		}
	}
}

// Only a value that could only be one of our codes is called misread.
func TestMisreadCode(t *testing.T) {
	cases := map[string]bool{
		"VOL204572":     false, // valid
		"LEC73048":      false, // valid
		"VOL204573":     true,  // wrong check digit
		"LEC73049":      true,
		"VOL20457":      true, // a digit missing
		"LEC730480":     true, // one too many
		"LEC":           false,
		"9782070408504": false,
		"VOLCAN":        false, // a word, for the title search
		"VOL-X0001":     false,
		"vol204572":     false,
		"":              false,
	}
	for in, want := range cases {
		if got := misreadCode(in); got != want {
			t.Errorf("misreadCode(%q) = %v, want %v", in, got, want)
		}
	}
}

// The prefixes stay distinct and clear of the letters AZERTY moves.
func TestPrefixes(t *testing.T) {
	if codePrefixCard == codePrefixCopy {
		t.Errorf("card and copy share the prefix %q", codePrefixCard)
	}
	for _, p := range []string{codePrefixCard, codePrefixCopy} {
		if len(p) != 3 {
			t.Errorf("prefix %q is not three letters", p)
		}
		if strings.ContainsAny(p, "AQZWM") {
			t.Errorf("prefix %q uses a letter AZERTY moves", p)
		}
	}
}
