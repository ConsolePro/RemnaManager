package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func TestNodeV2TemplateAndStableGeneratedSettings(t *testing.T) {
	j := provisionJob(t)
	j.Version = 2
	j.Params.ProfileName = "Berlin Edge"
	j.Params.SiteTemplate = "random"
	if err := prepareNewNodeSettings(&j); err != nil {
		t.Fatal(err)
	}
	beforeIDs := append([]string{}, j.ShortIDs...)
	beforeSite := j.Params.SiteTemplate
	if len(j.ShortIDs) < 3 || len(j.ShortIDs) > 6 {
		t.Fatal(j.ShortIDs)
	}
	if err := prepareNewNodeSettings(&j); err != nil || !reflect.DeepEqual(beforeIDs, j.ShortIDs) || beforeSite != j.Params.SiteTemplate {
		t.Fatal("settings changed on retry", err)
	}
	config := nodeRealityConfig(j)
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	if inbound["tag"] != "VLESS_REALITY_Berlin_Edge" {
		t.Fatal(inbound)
	}
	reality := inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
	if reality["xver"] != float64(1) || reality["target"] != "127.0.0.1:8443" || reality["privateKey"] != j.RealityKey {
		t.Fatal(reality)
	}
	seen := map[string]bool{}
	for _, id := range j.ShortIDs {
		if len(id) != 16 || seen[id] {
			t.Fatal("invalid or duplicate short ID")
		}
		seen[id] = true
	}
	if !reflect.DeepEqual(reality["shortIds"], j.ShortIDs) {
		t.Fatal("profile lost short IDs")
	}
}

func TestNodeProfileAndHostNamesRespectJournalVersion(t *testing.T) {
	for _, version := range []int{2, 3} {
		j := provisionJob(t)
		j.Version = version
		j.Params.ProfileName = "FN1001"
		profile, host := "FN1001", j.Params.Name
		if version == 3 {
			profile, host = "VLESS_REALITY_FN1001", "VLESS_REALITY_FN1001"
		}
		if nodeProfileName(j) != profile || nodeInboundTag(j) != "VLESS_REALITY_FN1001" || nodeHostName(j) != host {
			t.Fatal("incorrect profile, tag or default host", version)
		}
		j.Params.HostName = "Finland 🇫🇮"
		if nodeHostName(j) != j.Params.HostName {
			t.Fatal("custom host name lost")
		}
		if err := prepareNewNodeSettings(&j); err != nil {
			t.Fatal(err)
		}
		r, _ := managementFixture(t)
		if err := r.saveNodeJob(&j); err != nil {
			t.Fatal(err)
		}
		loaded, err := r.loadNodeJob(j.ID)
		if err != nil || nodeProfileName(loaded) != profile || nodeHostName(loaded) != j.Params.HostName {
			t.Fatal("names changed after resume", err)
		}
	}
}

func TestNodeNamesFitPanelLimits(t *testing.T) {
	for _, name := range []string{"FN1001", strings.Repeat("A", 16), "VLESS_REALITY_FN1001"} {
		if err := ValidateNodeShortProfileName(services.NodeInstallParameters{ProfileName: name}); err != nil {
			t.Fatal("valid short name rejected", name, err)
		}
	}
	if err := ValidateNodeShortProfileName(services.NodeInstallParameters{ProfileName: strings.Repeat("A", 17)}); err == nil {
		t.Fatal("oversized profile accepted")
	}
	j := provisionJob(t)
	j.Version = 3
	j.Params.ProfileName = strings.Repeat("A", 17)
	if err := prepareNewNodeSettings(&j); err == nil {
		t.Fatal("backend accepted oversized profile")
	}
	for _, host := range []string{strings.Repeat("A", 101), strings.Repeat("😀", 51), "\n", "host\tname"} {
		j.Params.HostName = host
		if err := validateNodeDetails(j.Params); err == nil {
			t.Fatal("invalid host accepted", host)
		}
	}
}

func TestPreparedNodeWebsitesHaveLocalWorkingPages(t *testing.T) {
	for _, name := range NodeSiteTemplates {
		data, err := nodeWebsiteArchive(name)
		if err != nil {
			t.Fatal(err)
		}
		zip, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		archive := tar.NewReader(zip)
		files := map[string]string{}
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(archive)
			if err != nil {
				t.Fatal(err)
			}
			files[header.Name] = string(body)
		}
		for _, file := range []string{"index.html", "about.html", "notes.html", "404.html", "style.css", "favicon.svg"} {
			if files[file] == "" {
				t.Fatal(name, "missing asset", file)
			}
		}
		if !strings.Contains(files["index.html"], "/notes.html") || strings.Contains(files["index.html"], "example.com") {
			t.Fatal("placeholder website")
		}
	}
}

func TestNodeCountryListMatchesPanelAndSearchesRussian(t *testing.T) {
	if len(NodeCountries()) < 190 || !ValidNodeCountry("DE") || ValidNodeCountry("ZZ") {
		t.Fatal("invalid country list")
	}
	result := FilterNodeCountries("Германия", "ru")
	if len(result) != 1 || result[0].Code != "DE" {
		t.Fatal(result)
	}
}

func TestNodeAPIPortAndDNSChecks(t *testing.T) {
	j := provisionJob(t)
	j.Params.SSH.Port = 2222
	j.Params.APIPort = 3333
	if err := ValidateNodeInstall(j.Params); err != nil {
		t.Fatal("custom API port rejected", err)
	}
	j.Params.APIPort = 2222
	if err := ValidateNodeInstall(j.Params); err == nil {
		t.Fatal("SSH/API collision accepted")
	}
	r := NewReal()
	if err := r.checkNodeDNS(context.Background(), services.NodeInstallParameters{Address: "192.0.2.1", Domain: "192.0.2.2"}); err == nil {
		t.Fatal("mismatched addresses accepted")
	}
	if err := r.checkNodeDNS(context.Background(), services.NodeInstallParameters{Address: "192.0.2.1", Domain: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.checkNodeDNS(context.Background(), services.NodeInstallParameters{Address: "192.0.2.1", Domain: "2001:db8::1"}, net.ParseIP("2001:db8::1")); err != nil {
		t.Fatal("valid second node address rejected", err)
	}
}

func TestNodeInterfaceIPsIgnoresSSHBanner(t *testing.T) {
	r, runner := managementFixture(t)
	j := provisionJob(t)
	runner.response = func(call string, _ []byte) ([]byte, error) {
		return []byte("Welcome to this server\nRM_NODE_IPS=[{\"addr_info\":[{\"local\":\"127.0.0.1\"},{\"local\":\"2001:db8::1\"},{\"local\":\"192.0.2.10\"}]}]\n"), nil
	}
	ips, err := r.nodeInterfaceIPs(context.Background(), j)
	if err != nil || len(ips) != 2 || !ips[0].Equal(net.ParseIP("2001:db8::1")) {
		t.Fatal(ips, err)
	}
}
