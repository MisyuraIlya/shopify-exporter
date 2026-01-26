package dto

type ShopifyMetafield struct {
	ID        string `json:"id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	Type      string `json:"type,omitempty"`
}

type MetafieldsSetData struct {
	MetafieldsSet struct {
		Metafields []ShopifyMetafield `json:"metafields,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"metafieldsSet"`
}

type MetafieldDefinitionNode struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key,omitempty"`
}

type MetafieldDefinitionConnection struct {
	Nodes    []MetafieldDefinitionNode `json:"nodes,omitempty"`
	PageInfo ShopifyPageInfo           `json:"pageInfo,omitempty"`
}

type MetafieldDefinitionsQueryData struct {
	MetafieldDefinitions MetafieldDefinitionConnection `json:"metafieldDefinitions"`
}

type MetafieldDefinitionCreateData struct {
	MetafieldDefinitionCreate struct {
		CreatedDefinition *MetafieldDefinitionNode `json:"createdDefinition,omitempty"`
		UserErrors        []ShopifyUserError       `json:"userErrors,omitempty"`
	} `json:"metafieldDefinitionCreate"`
}
