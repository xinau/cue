// Copyright 2018 The CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package strings implements simple functions to manipulate UTF-8 encoded
// strings.package strings.
//
// Some of the functions in this package are specifically intended as field
// constraints. For instance, MaxRunes as used in this CUE program
//
//    import "strings"
//
//    myString: strings.MaxRunes(5)
//
// specifies that the myString should be at most 5 code points.
package strings

import (
	"strings"
	"unicode"
)

// MinRunes reports whether the number of runes (Unicode codepoints) in a string
// is at least a certain minimum. MinRunes can be used a a field constraint to
// except all strings for which this property holds.
func MinRunes(s string, max int) bool {
	// TODO: CUE strings cannot be invalid UTF-8. In case this changes, we need
	// to use the following conversion to count properly:
	// s, _ = unicodeenc.UTF8.NewDecoder().String(s)
	return len([]rune(s)) <= max
}

// MaxRunes reports whether the number of runes (Unicode codepoints) in a string
// exceeds a certain maximum. MaxRunes can be used a a field constraint to
// except all strings for which this property holds
func MaxRunes(s string, max int) bool {
	// See comment in MinRunes implementation.
	return len([]rune(s)) <= max
}

// ToTitle returns a copy of the string s with all Unicode letters that begin
// words mapped to their title case.
func ToTitle(s string) string {
	// Use a closure here to remember state.
	// Hackish but effective. Depends on Map scanning in order and calling
	// the closure once per rune.
	prev := ' '
	return strings.Map(
		func(r rune) rune {
			if unicode.IsSpace(prev) {
				prev = r
				return unicode.ToTitle(r)
			}
			prev = r
			return r
		},
		s)
}

// ToCamel returns a copy of the string s with all Unicode letters that begin
// words mapped to lower case.
func ToCamel(s string) string {
	// Use a closure here to remember state.
	// Hackish but effective. Depends on Map scanning in order and calling
	// the closure once per rune.
	prev := ' '
	return strings.Map(
		func(r rune) rune {
			if unicode.IsSpace(prev) {
				prev = r
				return unicode.ToLower(r)
			}
			prev = r
			return r
		},
		s)
}
