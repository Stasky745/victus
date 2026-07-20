// Package openfoodfacts is a client for the public Open Food Facts API,
// used to search for and import packaged-food nutrition data by barcode or
// name. No authentication is required, but OFF's usage policy requires a
// descriptive User-Agent identifying the calling application.
package openfoodfacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	// defaultBaseURL is Open Food Facts's single public service — not a
	// per-deployment configuration like Mealie's. Overridable via
	// WithBaseURL, which production code never needs but tests do (to
	// point the client at an httptest.Server instead).
	defaultBaseURL = "https://world.openfoodfacts.org"

	// userAgent follows OFF's requested "AppName/Version (URL)" format —
	// required on every request, or OFF may rate-limit or reject it.
	userAgent = "Victus/1.0 (+https://github.com/Stasky745/victus)"

	requestTimeout = 10 * time.Second
)

// ErrNotFound is returned by GetByBarcode when OFF has no product for that
// barcode.
var ErrNotFound = errors.New("product not found")

// Client talks to the Open Food Facts API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option configures a Client constructed via New.
type Option func(*Client)

// WithBaseURL points the client at a different base URL than the real
// public API — production code never needs this; tests use it to point at
// an httptest.Server.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// New returns a Client for the public Open Food Facts API. No
// authentication is required or configurable.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// nutriments mirrors the subset of Open Food Facts's "nutriments" object
// Victus cares about. Every field is a pointer so a key OFF's contributors
// never filled in unmarshals to nil ("unknown"), not a misleading zero.
// OFF stores every _100g value in grams regardless of the nutrient's usual
// unit (confirmed against live product data: sodium_100g/cholesterol_100g
// both carry a "g"-valued companion sodium_unit/cholesterol_unit field) —
// NutrientAmounts converts the milligram-scale nutrients accordingly.
type nutriments struct {
	EnergyKcal100g    *float64 `json:"energy-kcal_100g"`
	Proteins100g      *float64 `json:"proteins_100g"`
	Carbohydrates100g *float64 `json:"carbohydrates_100g"`
	Fat100g           *float64 `json:"fat_100g"`
	SaturatedFat100g  *float64 `json:"saturated-fat_100g"`
	Fiber100g         *float64 `json:"fiber_100g"`
	Sugars100g        *float64 `json:"sugars_100g"`
	Sodium100g        *float64 `json:"sodium_100g"`      // grams
	Cholesterol100g   *float64 `json:"cholesterol_100g"` // grams
	Iron100g          *float64 `json:"iron_100g"`        // grams
}

const gramsToMilligrams = 1000

// NutrientAmounts maps the per-100g nutriments to Victus's nutrient
// registry keys (internal/db/migrations/00003_nutrients.sql). A nutrient
// OFF has no data for is simply omitted from the result — never coerced to
// zero — matching mealslib.MealInput.NutrientAmounts's "absent means
// unset" contract.
func (n nutriments) NutrientAmounts() map[string]float64 {
	out := make(map[string]float64, 10)
	addGrams := func(key string, v *float64) {
		if v != nil {
			out[key] = *v
		}
	}
	addMilligrams := func(key string, gramsVal *float64) {
		if gramsVal != nil {
			out[key] = *gramsVal * gramsToMilligrams
		}
	}
	addGrams("calories", n.EnergyKcal100g) // energy-kcal is already in kcal, not grams
	addGrams("protein_g", n.Proteins100g)
	addGrams("carbs_g", n.Carbohydrates100g)
	addGrams("fat_g", n.Fat100g)
	addGrams("saturated_fat_g", n.SaturatedFat100g)
	addGrams("fiber_g", n.Fiber100g)
	addGrams("sugar_g", n.Sugars100g)
	addMilligrams("sodium_mg", n.Sodium100g)
	addMilligrams("cholesterol_mg", n.Cholesterol100g)
	addMilligrams("iron_mg", n.Iron100g)
	return out
}

// Product is one Open Food Facts item, hydrated with its nutrition.
type Product struct {
	Barcode         string
	Name            string
	NutrientAmounts map[string]float64
}

// DisplayName returns p.Name, or a fallback identifying it by barcode if
// OFF's own product-name field is blank — data quality on OFF varies by
// contributor, and a nameless library entry is worse than an ugly one.
func (p Product) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return "Unnamed product (" + p.Barcode + ")"
}

// productJSON is the shape shared by both the barcode-lookup and the
// search-result product representations.
type productJSON struct {
	Code        string     `json:"code"`
	ProductName string     `json:"product_name"`
	Nutriments  nutriments `json:"nutriments"`
}

func (p productJSON) toProduct() Product {
	return Product{
		Barcode:         p.Code,
		Name:            p.ProductName,
		NutrientAmounts: p.Nutriments.NutrientAmounts(),
	}
}

// GetByBarcode looks up a single product by its barcode (EAN/UPC). Returns
// ErrNotFound if OFF has no product for that barcode.
func (c *Client) GetByBarcode(ctx context.Context, barcode string) (Product, error) {
	u := c.baseURL + "/api/v3/product/" + url.PathEscape(barcode) + ".json"

	var resp struct {
		Product productJSON `json:"product"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return Product{}, fmt.Errorf("get product %q: %w", barcode, err)
	}
	if resp.Product.Code == "" {
		return Product{}, ErrNotFound
	}
	return resp.Product.toProduct(), nil
}

// ProductURL returns the public product page for barcode — Victus stores
// this as the meal's RecipeURL, mirroring the mealie package's RecipeURL.
func (c *Client) ProductURL(barcode string) string {
	return c.baseURL + "/product/" + url.PathEscape(barcode)
}

// SearchByName finds products whose name matches query, via OFF's legacy
// search endpoint (/cgi/search.pl) — OFF's newer, officially-recommended
// full-text search lives in a separate "Search-a-licious" service
// (search.openfoodfacts.org) with its own evolving API; this project uses
// the older, longer-stable endpoint deliberately, isolated to this one
// function, so migrating later (if OFF fully retires it) is a contained
// change.
func (c *Client) SearchByName(ctx context.Context, query string) ([]Product, error) {
	u := c.baseURL + "/cgi/search.pl?" + url.Values{
		"search_terms": {query},
		"json":         {"1"},
		"page_size":    {"20"},
	}.Encode()

	// Products is decoded as raw JSON per-entry (not straight into
	// []productJSON) so one malformed entry can be skipped individually —
	// OFF's crowdsourced data quality varies by contributor, and a single
	// entry with, say, a numeric nutriment field sent as "" instead of
	// omitted would otherwise fail json.Unmarshal for the ENTIRE response,
	// losing every other perfectly good result in the same search.
	var resp struct {
		Products []json.RawMessage `json:"products"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("search products: %w", err)
	}

	products := make([]Product, 0, len(resp.Products))
	for _, raw := range resp.Products {
		var p productJSON
		if err := json.Unmarshal(raw, &p); err != nil {
			continue // skip this one malformed entry, not the whole search
		}
		if p.Code == "" {
			continue // defensive: skip any structurally-valid-but-empty entry too
		}
		products = append(products, p.toProduct())
	}
	return products, nil
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, u)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
