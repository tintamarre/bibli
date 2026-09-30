package main

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"strings"
)

// The internal codes printed as barcodes on cards and spine labels. Drawn
// at random, so a card does not publish how many pupils are on file. The prefix
// must stay clear of A, Q, Z, W and M, which an AZERTY keyboard moves.
const (
	codePrefixCard = "LEC" // lector
	codePrefixCopy = "VOL" // volumen

	// Random digits before the check digit.
	cardDigits = 4
	copyDigits = 5
)

// dammTable is the order-10 quasigroup of Damm's check digit, which catches
// every single wrong digit and every swap of two adjacent ones.
var dammTable = [10][10]byte{
	{0, 3, 1, 7, 5, 9, 8, 6, 4, 2},
	{7, 0, 9, 2, 1, 5, 4, 8, 6, 3},
	{4, 2, 0, 6, 8, 7, 1, 3, 5, 9},
	{1, 7, 5, 0, 9, 8, 3, 4, 2, 6},
	{6, 1, 2, 3, 0, 4, 5, 9, 7, 8},
	{3, 6, 7, 4, 2, 0, 9, 5, 8, 1},
	{5, 8, 6, 9, 7, 2, 0, 1, 3, 4},
	{8, 9, 4, 5, 3, 6, 2, 0, 1, 7},
	{9, 4, 3, 8, 6, 1, 7, 2, 0, 5},
	{2, 5, 8, 1, 4, 3, 6, 7, 9, 0},
}

// damm folds a string of digits into its interim digit: the check digit to
// append, or 0 when the check digit is already on the end.
func damm(digits string) byte {
	var interim byte
	for i := 0; i < len(digits); i++ {
		interim = dammTable[interim][digits[i]-'0']
	}
	return interim
}

func newCode(prefix string, digits int) string {
	n := 1
	for i := 0; i < digits; i++ {
		n *= 10
	}
	body := fmt.Sprintf("%0*d", digits, rand.IntN(n))
	return prefix + body + string('0'+damm(body))
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// misreadCode says whether s has our prefix and only digits yet is not a code
// we could have printed: the desk says "mistyped" rather than "unknown".
func misreadCode(s string) bool {
	for _, c := range []struct {
		prefix string
		digits int
	}{{codePrefixCard, cardDigits}, {codePrefixCopy, copyDigits}} {
		rest, ok := strings.CutPrefix(s, c.prefix)
		if !ok || !allDigits(rest) {
			continue
		}
		return len(rest) != c.digits+1 || damm(rest) != 0
	}
	return false
}

// Draws before giving up; the UNIQUE columns catch a collision regardless.
const codeAttempts = 20

// freshCopyCode draws a code no copy holds yet. Call inside the transaction
// that does the INSERT, so a code drawn earlier in it is seen.
func freshCopyCode(tx *sql.Tx) (string, error) {
	return freshCode(tx, codePrefixCopy, copyDigits, `SELECT COUNT(*) FROM copy WHERE code = ?`)
}

// freshCardCode draws a card code no borrower holds yet; pupils and teachers
// share the prefix.
func freshCardCode(tx *sql.Tx) (string, error) {
	return freshCode(tx, codePrefixCard, cardDigits, `SELECT COUNT(*) FROM borrower WHERE card_code = ?`)
}

func freshCode(tx *sql.Tx, prefix string, digits int, taken string) (string, error) {
	for i := 0; i < codeAttempts; i++ {
		code := newCode(prefix, digits)
		var n int
		if err := tx.QueryRow(taken, code).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return code, nil
		}
	}
	return "", fmt.Errorf("no free %s code after %d draws", prefix, codeAttempts)
}
