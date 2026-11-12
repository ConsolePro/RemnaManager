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

func nodeCountryHostPrefix(j NodeJob) string {
	if j.Version < 4 {
		return ""
	}
	return NodeCountryHostPrefix(j.Params.CountryCode, j.Params.HostLanguage)
}

// NodeCountryHostPrefix formats the country selected in the wizard for subscriptions.
func NodeCountryHostPrefix(code, lang string) string {
	for _, country := range NodeCountries() {
		if country.Code != code || code == "XX" {
			continue
		}
		name := country.Name
		if lang != "en" {
			if region, err := language.ParseRegion(code); err == nil {
				name = display.Russian.Regions().Name(region)
			}
		}
		flag := string([]rune{rune(0x1f1e6 + int(code[0]-'A')), rune(0x1f1e6 + int(code[1]-'A'))})
		return flag + " " + name + " · "
	}
	return ""
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
