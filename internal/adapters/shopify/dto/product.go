package dto

type ProductPayload struct {
	Title    string           `json:"title"`
	BodyHTML string           `json:"body_html,omitempty"`
	Status   string           `json:"status,omitempty"`
	Variants []VariantPayload `json:"variants"`
}

type VariantPayload struct {
	SKU     string `json:"sku,omitempty"`
	Barcode string `json:"barcode,omitempty"`
}

type GraphQLResponse[T any] struct {
	Data   T              `json:"data"`
	Errors []GraphQLError `json:"errors,omitempty"`
}

type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []any                  `json:"path,omitempty"`
	Extensions map[string]any         `json:"extensions,omitempty"`
	Locations  []GraphQLErrorLocation `json:"locations,omitempty"`
}

type GraphQLErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type ShopifyUserError struct {
	Field   []string `json:"field,omitempty"`
	Message string   `json:"message"`
}

type ShopifyProduct struct {
	ID              string `json:"id,omitempty"`
	Title           string `json:"title,omitempty"`
	Handle          string `json:"handle,omitempty"`
	DescriptionHTML string `json:"descriptionHtml,omitempty"`
	Status          string `json:"status,omitempty"`

	Vendor      string   `json:"vendor,omitempty"`
	ProductType string   `json:"productType,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`

	Options  []ShopifyProductOption   `json:"options,omitempty"`
	Variants ShopifyVariantConnection `json:"variants,omitempty"`
	Media    ShopifyMediaConnection   `json:"media,omitempty"`
}

type ShopifyProductOption struct {
	ID     string   `json:"id,omitempty"`
	Name   string   `json:"name,omitempty"`
	Values []string `json:"values,omitempty"`
}

type ShopifyPageInfo struct {
	HasNextPage bool   `json:"hasNextPage,omitempty"`
	EndCursor   string `json:"endCursor,omitempty"`
}

type ShopifyProductConnection struct {
	Edges    []ShopifyProductEdge `json:"edges,omitempty"`
	Nodes    []ShopifyProduct     `json:"nodes,omitempty"`
	PageInfo ShopifyPageInfo      `json:"pageInfo,omitempty"`
}

type ShopifyProductEdge struct {
	Cursor string         `json:"cursor,omitempty"`
	Node   ShopifyProduct `json:"node"`
}

type ShopifyVariantConnection struct {
	Edges    []ShopifyVariantEdge `json:"edges,omitempty"`
	Nodes    []ShopifyVariant     `json:"nodes,omitempty"`
	PageInfo ShopifyPageInfo      `json:"pageInfo,omitempty"`
}

type ShopifyVariantEdge struct {
	Cursor string         `json:"cursor,omitempty"`
	Node   ShopifyVariant `json:"node"`
}

type ShopifyVariant struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	SKU     string `json:"sku,omitempty"`
	Barcode string `json:"barcode,omitempty"`

	Price          string `json:"price,omitempty"`
	CompareAtPrice string `json:"compareAtPrice,omitempty"`

	InventoryQuantity *int `json:"inventoryQuantity,omitempty"`
}

type ShopifyMediaConnection struct {
	Edges    []ShopifyMediaEdge `json:"edges,omitempty"`
	Nodes    []ShopifyMedia     `json:"nodes,omitempty"`
	PageInfo ShopifyPageInfo    `json:"pageInfo,omitempty"`
}

type ShopifyMediaEdge struct {
	Node ShopifyMedia `json:"node"`
}

type ShopifyMedia struct {
	ID               string        `json:"id,omitempty"`
	MediaContentType string        `json:"mediaContentType,omitempty"`
	Image            *ShopifyImage `json:"image,omitempty"`
}

type ShopifyImage struct {
	URL     string `json:"url,omitempty"`
	AltText string `json:"altText,omitempty"`
}

type ProductsQueryData struct {
	Products ShopifyProductConnection `json:"products"`
}

type ProductCreateData struct {
	ProductCreate ProductCreatePayload `json:"productCreate"`
}

type ProductCreatePayload struct {
	Product    *ShopifyProduct    `json:"product"`
	UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
}

type TranslationsRegisterData struct {
	TranslationsRegister TranslationsRegisterPayload `json:"translationsRegister"`
}

type TranslationsRegisterPayload struct {
	UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
}
