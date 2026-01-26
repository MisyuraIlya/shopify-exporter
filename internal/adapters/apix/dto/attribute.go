package dto

type AttributesResponse struct {
	API                     string             `json:"api,omitempty"`
	Status                  string             `json:"status,omitempty"`
	AttributesMainCount     int                `json:"attributesMainCount,omitempty"`
	AttributesSubCount      int                `json:"attributesSubCount,omitempty"`
	AttributesProductsCount int                `json:"attributesProductsCount,omitempty"`
	AttributesMain          []AttributeMain    `json:"attributesMain,omitempty"`
	AttributesProducts      []AttributeProduct `json:"attributesProducts,omitempty"`
}

type AttributeMain struct {
	NoteName        string         `json:"NoteName,omitempty"`
	NoteNameEnglish string         `json:"NoteNameEnglish,omitempty"`
	NoteID          int            `json:"NoteID,omitempty"`
	ItemFlag        int            `json:"ItemFlag,omitempty"`
	NumSort         int            `json:"NumSort,omitempty"`
	AttributesSub   []AttributeSub `json:"attributesSub,omitempty"`
}

type AttributeSub struct {
	Note        string `json:"Note,omitempty"`
	NoteEnglish string `json:"NoteEnglish,omitempty"`
	NoteID      int    `json:"NoteID,omitempty"`
}

type AttributeProduct struct {
	ID          int    `json:"ID,omitempty"`
	KeF         string `json:"KeF,omitempty"`
	Note        string `json:"Note,omitempty"`
	ItemFlag    int    `json:"ItemFlag,omitempty"`
	NoteID      int    `json:"NoteID,omitempty"`
	Dumi        int    `json:"Dumi,omitempty"`
	NoteEnglish string `json:"NoteEnglish,omitempty"`
}
