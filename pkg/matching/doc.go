// Package matching implements price-time priority matching as a pure function
// of an incoming order and an ordered maker cursor. It does not read Cosmos
// SDK storage and it does not mutate the book.
package matching
