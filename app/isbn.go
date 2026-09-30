package main

import (
	"errors"
	"strings"
)

// Books published before 2007 carry an ISBN-10, and many records (the BnF's
// included) are indexed only in that form. The scanned barcode is always an
// EAN-13. Both forms must therefore be computed, and catalogues queried with
// each of them.
//
// Special case: the 979 prefix has NO ISBN-10 equivalent. To10 returns an
// empty string in that case, which is not an error.

var ErrInvalidISBN = errors.New("invalid ISBN")

// NormaliseISBN keeps only the digits and a trailing X.
// Accepts hyphens, spaces and an "ISBN" prefix.
func NormaliseISBN(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'X' || r == 'x':
			b.WriteRune('X')
		}
	}
	return b.String()
}

// checkDigit10 computes an ISBN-10 check digit from its first 9 digits.
// Weighted sum 10..2, modulo 11, remainder 10 written as 'X'.
func checkDigit10(base string) byte {
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(base[i]-'0') * (10 - i)
	}
	remainder := (11 - sum%11) % 11
	if remainder == 10 {
		return 'X'
	}
	return byte('0' + remainder)
}

// checkDigit13 computes an ISBN-13 check digit from its first 12 digits.
// Weighted sum 1,3,1,3..., modulo 10.
func checkDigit13(base string) byte {
	sum := 0
	for i := 0; i < 12; i++ {
		weight := 1
		if i%2 == 1 {
			weight = 3
		}
		sum += int(base[i]-'0') * weight
	}
	return byte('0' + (10-sum%10)%10)
}

func ISBN10Valid(s string) bool {
	if len(s) != 10 {
		return false
	}
	for i := 0; i < 9; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s[9] == checkDigit10(s)
}

func ISBN13Valid(s string) bool {
	if len(s) != 13 {
		return false
	}
	for i := 0; i < 13; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	if !strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979") {
		return false
	}
	return s[12] == checkDigit13(s)
}

// To13 converts an ISBN-10 to an ISBN-13 by prefixing 978.
func To13(isbn10 string) string {
	if !ISBN10Valid(isbn10) {
		return ""
	}
	base := "978" + isbn10[:9]
	return base + string(checkDigit13(base))
}

// To10 converts an ISBN-13 to an ISBN-10.
// Returns "" for a 979 prefix, which has no ISBN-10 equivalent.
func To10(isbn13 string) string {
	if !ISBN13Valid(isbn13) || !strings.HasPrefix(isbn13, "978") {
		return ""
	}
	base := isbn13[3:12]
	return base + string(checkDigit10(base))
}

// withoutAddOn drops the 2- or 5-digit price code some USB scanners send
// glued to the EAN-13 of a book.
func withoutAddOn(s string) string {
	if (len(s) == 15 || len(s) == 18) &&
		(strings.HasPrefix(s, "978") || strings.HasPrefix(s, "979")) &&
		ISBN13Valid(s[:13]) {
		return s[:13]
	}
	return s
}

// ISBNForms takes whatever comes out of the scanner or the keyboard and
// returns both canonical forms. isbn10 may be empty (979 prefix).
//
// Call this before querying any catalogue and before writing to the database.
func ISBNForms(scan string) (isbn13, isbn10 string, err error) {
	s := withoutAddOn(NormaliseISBN(scan))

	switch {
	case ISBN13Valid(s):
		return s, To10(s), nil
	case ISBN10Valid(s):
		return To13(s), s, nil
	default:
		return "", "", ErrInvalidISBN
	}
}

// FormISBNs re-derives both forms from a record form's hidden fields, which
// come back from the browser. Both empty is a book without an ISBN.
func FormISBNs(isbn13, isbn10 string) (string, string, error) {
	s := strings.TrimSpace(isbn13)
	if s == "" {
		s = strings.TrimSpace(isbn10)
	}
	if s == "" {
		return "", "", nil
	}
	return ISBNForms(s)
}
