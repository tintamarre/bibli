package main

import "testing"

// Vectors checked by hand. Real ISBNs are kept where possible:
//   - 978 : Le Petit Prince (Gallimard) → 2070408507 / 9782070408504
//   - X   : 080442957X -> 9780804429573 (check digit 10, written "X")
//   - 979 : a prefix with no ISBN-10 equivalent, isbn10 must stay empty
func TestISBNForms(t *testing.T) {
	cases := []struct {
		name    string
		scan    string
		isbn13  string
		isbn10  string
		wantErr bool
	}{
		{"bare ean13", "9782070408504", "9782070408504", "2070408507", false},
		{"ean13 with hyphens", "978-2-07-040850-4", "9782070408504", "2070408507", false},
		{"ean13 with an ISBN prefix and spaces", "ISBN 978 2 07 040850 4", "9782070408504", "2070408507", false},
		{"bare isbn10 -> both forms", "2070408507", "9782070408504", "2070408507", false},
		{"isbn10 with check digit X", "080442957X", "9780804429573", "080442957X", false},
		{"isbn10 with lowercase x", "080442957x", "9780804429573", "080442957X", false},
		{"979 prefix: no isbn10", "9791234567896", "9791234567896", "", false},
		{"ean13 + EAN-5 price code", "978207040850490000", "9782070408504", "2070408507", false},
		{"ean13 + EAN-2 add-on", "978207040850412", "9782070408504", "2070408507", false},
		{"979 + EAN-5", "979123456789654999", "9791234567896", "", false},

		{"wrong check digit", "9782070408505", "", "", true},
		{"isbn10 with a wrong check digit", "2070408508", "", "", true},
		{"too short (EAN-5 price)", "54999", "", "", true},
		{"add-on after a wrong check digit", "978207040850590000", "", "", true},
		{"18 digits that are not a book", "123456789012345678", "", "", true},
		{"empty", "", "", "", true},
		{"noise only", "abc-def", "", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i13, i10, err := ISBNForms(c.scan)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want an error for %q, got i13=%q i10=%q", c.scan, i13, i10)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.scan, err)
			}
			if i13 != c.isbn13 {
				t.Errorf("isbn13: want %q, got %q", c.isbn13, i13)
			}
			if i10 != c.isbn10 {
				t.Errorf("isbn10: want %q, got %q", c.isbn10, i10)
			}
		})
	}
}

func TestConversionsAreReversible(t *testing.T) {
	// A 978 must survive the round trip.
	const i10 = "2070408507"
	i13 := To13(i10)
	if i13 != "9782070408504" {
		t.Fatalf("To13(%q) = %q", i10, i13)
	}
	if back := To10(i13); back != i10 {
		t.Fatalf("To10(%q) = %q, want %q", i13, back, i10)
	}
}

func TestTo10On979IsEmpty(t *testing.T) {
	if got := To10("9791234567896"); got != "" {
		t.Fatalf("To10 on a 979 prefix must be empty, got %q", got)
	}
}

// A badly calibrated scanner produces partial or garbled reads, and the check
// digit is what catches them — before any network call.
func TestISBN13ValidRejectsWhatIsNotAnISBN(t *testing.T) {
	cases := map[string]bool{
		"9782070408504": true,
		"9791036300271": true,
		// A 13-digit EAN that is not a book: no 978/979 prefix.
		"5449000000996": false,
		// A letter in the middle, from a partial read.
		"97820704085O4": false,
		// The X of an ISBN-10 has no place in an EAN-13.
		"978207040850X":  false,
		"":               false,
		"978207040850":   false, // one digit short
		"97820704085041": false,
	}
	for in, want := range cases {
		if got := ISBN13Valid(in); got != want {
			t.Errorf("ISBN13Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestISBN10ValidRejectsWhatIsNotAnISBN(t *testing.T) {
	cases := map[string]bool{
		"2070408507":  true,
		"080442957X":  true,
		"20704085O7":  false, // letter O for zero
		"X070408507":  false, // X anywhere but last
		"207040850":   false,
		"20704085077": false,
		"":            false,
	}
	for in, want := range cases {
		if got := ISBN10Valid(in); got != want {
			t.Errorf("ISBN10Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

// The conversions refuse rather than invent: an invalid input gives an empty
// string, which the caller treats as "this form does not exist".
func TestConversionsRefuseAnInvalidInput(t *testing.T) {
	for _, bad := range []string{"", "2070408508", "not an isbn", "9782070408504"} {
		if got := To13(bad); got != "" {
			t.Errorf("To13(%q) = %q, want empty", bad, got)
		}
	}
	for _, bad := range []string{"", "9782070408505", "2070408507"} {
		if got := To10(bad); got != "" {
			t.Errorf("To10(%q) = %q, want empty", bad, got)
		}
	}
}

// NormaliseISBN keeps only what a check digit can be computed on: a scanner may
// send the hyphens of the printed ISBN, and a person types spaces.
func TestNormaliseISBN(t *testing.T) {
	cases := map[string]string{
		"978-2-07-040850-4":    "9782070408504",
		" ISBN 2-07-040850-7 ": "2070408507",
		"080442957x":           "080442957X",
		"EAN\t978 2070408504":  "9782070408504",
		"":                     "",
		"aucun chiffre ici":    "",
	}
	for in, want := range cases {
		if got := NormaliseISBN(in); got != want {
			t.Errorf("NormaliseISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

// A record form's hidden fields come back from the browser: they are checked
// like a scan, and the pair is rebuilt rather than trusted.
func TestFormISBNs(t *testing.T) {
	cases := []struct{ i13, i10, want13, want10 string }{
		{"9782070408504", "", "9782070408504", "2070408507"},
		{"", "2070408507", "9782070408504", "2070408507"},
		{"9782070408504", "0000000000", "9782070408504", "2070408507"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		i13, i10, err := FormISBNs(c.i13, c.i10)
		if err != nil || i13 != c.want13 || i10 != c.want10 {
			t.Errorf("FormISBNs(%q, %q) = %q, %q, %v; want %q, %q", c.i13, c.i10, i13, i10, err, c.want13, c.want10)
		}
	}
	for _, bad := range []string{"../../static/app", "9782070408505"} {
		if _, _, err := FormISBNs(bad, ""); err == nil {
			t.Errorf("FormISBNs(%q) accepted", bad)
		}
	}
}
