package model

type Attribute struct {
	ID          int
	HebrewName  string
	EnglishName string
	Values      []AttributeValue
}

type AttributeValue struct {
	ID          int
	HebrewName  string
	EnglishName string
}

type AttributeProduct struct {
	Sku          string
	AttributeID  int
	ValueHebrew  string
	ValueEnglish string
}
