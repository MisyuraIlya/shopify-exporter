package model

type Category struct {
	TitleHebrew   string
	TitlteEnglish string
}

type ProductCategories struct {
	SKU        string
	Categproes []Category
}
