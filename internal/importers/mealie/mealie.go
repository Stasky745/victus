// Package mealie is a client for a self-hosted Mealie instance's REST API,
// used to search for and import recipes (name + nutrition; Victus never
// stores the recipe/instructions themselves — see mealslib's RecipeURL).
package mealie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const requestTimeout = 10 * time.Second

// ErrNotFound is returned by GetRecipe when the slug doesn't exist on the
// configured Mealie instance (a stale search result, a recipe deleted
// between search and import).
var ErrNotFound = errors.New("recipe not found")

// Client talks to one Mealie instance, authenticated with a long-lived API
// token (generated in Mealie's own UI under Settings > API Tokens).
type Client struct {
	baseURL    string // no trailing slash
	apiKey     string
	httpClient *http.Client
}

// New returns a Client for the Mealie instance at baseURL.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// SearchResult is one recipe from a search — enough to list results and let
// the user pick one to import; full nutrition needs a follow-up GetRecipe
// call, since Mealie's list endpoint doesn't include it.
type SearchResult struct {
	Slug string
	Name string
}

// Search finds recipes on the configured Mealie instance whose name matches
// query.
func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	u := c.baseURL + "/api/recipes?" + url.Values{
		"search":  {query},
		"perPage": {"25"},
	}.Encode()

	var resp struct {
		Items []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("search recipes: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Items))
	for _, item := range resp.Items {
		results = append(results, SearchResult{Slug: item.Slug, Name: item.Name})
	}
	return results, nil
}

// RecipeNutrition mirrors Mealie's own nutrition schema: every field is a
// string, and blank/absent means "not recorded" rather than a real zero —
// callers should use NutrientAmounts rather than parsing these directly.
type RecipeNutrition struct {
	Calories            string `json:"calories"`
	ProteinContent      string `json:"proteinContent"`
	FatContent          string `json:"fatContent"`
	SaturatedFatContent string `json:"saturatedFatContent"`
	CarbohydrateContent string `json:"carbohydrateContent"`
	FiberContent        string `json:"fiberContent"`
	SugarContent        string `json:"sugarContent"`
	SodiumContent       string `json:"sodiumContent"`
	CholesterolContent  string `json:"cholesterolContent"`
}

// NutrientAmounts maps n's fields to Victus's nutrient registry keys
// (internal/db/migrations/00003_nutrients.sql). A blank or unparseable
// field is simply omitted from the result, matching Mealie's own "not
// recorded" semantics — never coerced to a misleading zero. Victus has no
// equivalent of Mealie's trans/unsaturated fat fields, so those are dropped;
// there's also no Mealie field for Victus's iron_mg.
func (n RecipeNutrition) NutrientAmounts() map[string]float64 {
	out := make(map[string]float64, 9)
	add := func(key, raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return
		}
		// strconv.ParseFloat accepts the literal strings "NaN"/"Inf" as
		// valid input — without this check, a corrupted value from Mealie
		// wouldn't be "omitted" like every other unparseable field (per
		// this function's own contract); it would sail through here and
		// only fail much later, at the DB-write boundary, aborting the
		// entire import instead of just this one nutrient.
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		out[key] = v
	}
	add("calories", n.Calories)
	add("protein_g", n.ProteinContent)
	add("fat_g", n.FatContent)
	add("saturated_fat_g", n.SaturatedFatContent)
	add("carbs_g", n.CarbohydrateContent)
	add("fiber_g", n.FiberContent)
	add("sugar_g", n.SugarContent)
	add("sodium_mg", n.SodiumContent)
	add("cholesterol_mg", n.CholesterolContent)
	return out
}

// Recipe is a Mealie recipe's detail, hydrated with its nutrition.
type Recipe struct {
	Slug      string
	Name      string
	Nutrition RecipeNutrition
}

// GetRecipe fetches slug's full detail, including nutrition. Returns
// ErrNotFound if the Mealie instance has no recipe with that slug.
func (c *Client) GetRecipe(ctx context.Context, slug string) (Recipe, error) {
	u := c.baseURL + "/api/recipes/" + url.PathEscape(slug)

	var resp struct {
		Slug      string          `json:"slug"`
		Name      string          `json:"name"`
		Nutrition RecipeNutrition `json:"nutrition"`
	}
	if err := c.getJSON(ctx, u, &resp); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Recipe{}, ErrNotFound
		}
		return Recipe{}, fmt.Errorf("get recipe %q: %w", slug, err)
	}
	return Recipe{Slug: resp.Slug, Name: resp.Name, Nutrition: resp.Nutrition}, nil
}

// RecipeURL returns the browser-facing page for slug on this Mealie
// instance — Victus stores this as the meal's RecipeURL (the way back to
// the actual recipe/instructions, which Victus itself never stores).
func (c *Client) RecipeURL(slug string) string {
	return c.baseURL + "/recipe/" + url.PathEscape(slug)
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
