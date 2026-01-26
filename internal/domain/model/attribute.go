package model

type Attribute struct {
	HebrewName  string
	EnglishName string
}

type AttributeProduct struct {
	Sku                  string
	AttributeNameHebrew  string
	AttributeNameEnglish string
}
