package installer

import (
	"encoding/json"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
	"strings"
)

type NodeCountry struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func NodeCountries() []NodeCountry {
	data, _ := nodeAssets.ReadFile("node_assets/countries.json")
	var countries []NodeCountry
	if err := json.Unmarshal(data, &countries); err != nil {
		panic("invalid embedded country list")
	}
	return countries
}
func ValidNodeCountry(code string) bool {
	for _, country := range NodeCountries() {
		if country.Code == code {
			return true
		}
	}
	return false
}
func (c NodeCountry) Label(lang string) string {
	name := c.Name
	if lang != "en" {
		if c.Code == "XX" {
			name = "Не указана"
		} else if region, err := language.ParseRegion(c.Code); err == nil {
			name = display.Russian.Regions().Name(region)
		}
	}
	return c.Code + " · " + name
}
func FilterNodeCountries(query, lang string) []NodeCountry {
	query = strings.ToLower(strings.TrimSpace(query))
	var result []NodeCountry
	for _, country := range NodeCountries() {
		if strings.Contains(strings.ToLower(country.Label(lang)+" "+country.Name), query) {
			result = append(result, country)
		}
	}
	return result
}
