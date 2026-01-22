package dto

type MarketRegionNode struct {
	Code string `json:"code,omitempty"`
}

type MarketCurrencySettings struct {
	BaseCurrency struct {
		CurrencyCode string `json:"currencyCode,omitempty"`
	} `json:"baseCurrency,omitempty"`
	LocalCurrencies bool `json:"localCurrencies,omitempty"`
}

type MarketNode struct {
	ID               string                 `json:"id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Handle           string                 `json:"handle,omitempty"`
	Enabled          bool                   `json:"enabled,omitempty"`
	CurrencySettings MarketCurrencySettings `json:"currencySettings,omitempty"`
	Regions          struct {
		Nodes []MarketRegionNode `json:"nodes,omitempty"`
	} `json:"regions,omitempty"`
}

type MarketsQueryData struct {
	Markets struct {
		Nodes    []MarketNode    `json:"nodes,omitempty"`
		PageInfo ShopifyPageInfo `json:"pageInfo,omitempty"`
	} `json:"markets"`
}

type MarketCreateData struct {
	MarketCreate struct {
		Market     *MarketNode        `json:"market,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"marketCreate"`
}

type MarketUpdateData struct {
	MarketUpdate struct {
		Market     *MarketNode        `json:"market,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"marketUpdate"`
}

type CatalogNode struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}

type CatalogsQueryData struct {
	Catalogs struct {
		Nodes []CatalogNode `json:"nodes,omitempty"`
	} `json:"catalogs"`
}

type CatalogCreateData struct {
	CatalogCreate struct {
		Catalog    *CatalogNode       `json:"catalog,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"catalogCreate"`
}

type MarketCatalogsData struct {
	Market *struct {
		ID       string `json:"id,omitempty"`
		Catalogs struct {
			Nodes    []CatalogNode   `json:"nodes,omitempty"`
			PageInfo ShopifyPageInfo `json:"pageInfo,omitempty"`
		} `json:"catalogs,omitempty"`
	} `json:"market,omitempty"`
}

type PublicationNode struct {
	ID          string `json:"id,omitempty"`
	AutoPublish bool   `json:"autoPublish,omitempty"`
}

type PriceListNode struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Currency string `json:"currency,omitempty"`
}

type CatalogDetailsData struct {
	Catalog *struct {
		ID          string           `json:"id,omitempty"`
		Title       string           `json:"title,omitempty"`
		Publication *PublicationNode `json:"publication,omitempty"`
		PriceList   *PriceListNode   `json:"priceList,omitempty"`
	} `json:"catalog,omitempty"`
}

type PublicationCreateData struct {
	PublicationCreate struct {
		Publication *PublicationNode   `json:"publication,omitempty"`
		UserErrors  []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"publicationCreate"`
}

type PublicationUpdateData struct {
	PublicationUpdate struct {
		Publication *PublicationNode   `json:"publication,omitempty"`
		UserErrors  []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"publicationUpdate"`
}

type PriceListCreateData struct {
	PriceListCreate struct {
		PriceList  *PriceListNode     `json:"priceList,omitempty"`
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"priceListCreate"`
}

type PriceListFixedPricesAddData struct {
	PriceListFixedPricesAdd struct {
		UserErrors []ShopifyUserError `json:"userErrors,omitempty"`
	} `json:"priceListFixedPricesAdd"`
}

type ProductVariantProductData struct {
	ProductVariant *struct {
		ID      string `json:"id,omitempty"`
		Product struct {
			ID string `json:"id,omitempty"`
		} `json:"product,omitempty"`
	} `json:"productVariant,omitempty"`
}
