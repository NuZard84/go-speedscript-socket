// models/typing_sentence.go
package models

type TypingSentence struct {
	Story           string `bson:"story"`
	TotalCharacters int    `bson:"totalCharacters"`
	TotalWords      int    `bson:"totalWords"`
	Hash            string `bson:"hash"`
}
