package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.yaml.in/yaml/v2"
)

var exprMetric = regexp.MustCompile(`\b(xray|vmagent)_[a-z0-9_]+`)

func exportedNames(t *testing.T) map[string]bool {
	t.Helper()
	e := newTestExporter(t, "127.0.0.1:1", func(c *Config) { c.Balancers = []string{"pool"} })
	ch := make(chan *prometheus.Desc, 256)
	go func() { e.Describe(ch); close(ch) }()
	names := map[string]bool{}
	fq := regexp.MustCompile(`fqName: "([^"]+)"`)
	for d := range ch {
		if m := fq.FindStringSubmatch(d.String()); m != nil {
			names[m[1]] = true
		}
	}
	return names
}

func dashboardExprs(t *testing.T, raw []byte) []string {
	t.Helper()
	var d struct {
		UID    string `json:"uid"`
		Panels []struct {
			Type    string `json:"type"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
		Templating struct {
			List []struct {
				Definition string `json:"definition"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.UID == "" {
		t.Error("dashboard without uid")
	}
	var exprs []string
	for _, p := range d.Panels {
		if p.Type == "row" {
			continue
		}
		if len(p.Targets) == 0 {
			t.Errorf("%s: %s panel without targets", d.UID, p.Type)
		}
		for _, tg := range p.Targets {
			if strings.TrimSpace(tg.Expr) == "" {
				t.Errorf("%s: empty expression", d.UID)
			}
			exprs = append(exprs, tg.Expr)
		}
	}
	for _, v := range d.Templating.List {
		exprs = append(exprs, v.Definition)
	}
	return exprs
}

var dsRef = regexp.MustCompile(`"datasource": \{\s*"type": "([^"]+)",\s*"uid": "([^"]+)"`)

func checkDatasources(t *testing.T, file string, raw []byte) {
	t.Helper()
	var d struct {
		Templating struct {
			List []struct {
				Name  string `json:"name"`
				Type  string `json:"type"`
				Query any    `json:"query"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, v := range d.Templating.List {
		if v.Type == "datasource" {
			want, _ = v.Query.(string)
		}
	}
	if want == "" {
		t.Fatalf("%s: no datasource variable", file)
	}
	refs := dsRef.FindAllSubmatch(raw, -1)
	if len(refs) == 0 {
		t.Errorf("%s: no datasource references", file)
	}
	for _, r := range refs {
		if string(r[1]) != want || string(r[2]) != "${datasource}" {
			t.Errorf("%s: datasource %s/%s, want %s/${datasource}", file, r[1], r[2], want)
		}
	}
}

func TestDashboards(t *testing.T) {
	t.Parallel()
	known := exportedNames(t)
	files, err := filepath.Glob("dashboards/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards: %v", err)
	}
	ipv4 := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if ipv4.Match(raw) {
			t.Errorf("%s contains an IPv4 address", f)
		}
		checkDatasources(t, f, raw)
		for _, expr := range dashboardExprs(t, raw) {
			for _, name := range exprMetric.FindAllString(expr, -1) {
				if strings.HasPrefix(name, "vmagent_") {
					continue
				}
				if !known[name] {
					t.Errorf("%s: %q uses unknown metric %s", f, expr, name)
				}
			}
		}
	}
}

func TestAlertRules(t *testing.T) {
	t.Parallel()
	known := exportedNames(t)
	raw, err := os.ReadFile("alerts/xray.rules.yml")
	if err != nil {
		t.Fatal(err)
	}
	var rules struct {
		Groups []struct {
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			n++
			if r.Alert == "" || r.Expr == "" {
				t.Errorf("incomplete rule %+v", r)
			}
			for _, name := range exprMetric.FindAllString(r.Expr, -1) {
				if !strings.HasPrefix(name, "vmagent_") && !known[name] {
					t.Errorf("%s uses unknown metric %s", r.Alert, name)
				}
			}
		}
	}
	if n == 0 {
		t.Error("no rules")
	}
}

func TestDashboardsGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generator")
	}
	dir := t.TempDir()
	cmd := exec.Command("go", "run", "dashboards/gen.go", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, out)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := filepath.Glob("dashboards/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 || len(files) != len(committed) {
		t.Fatalf("generator wrote %d dashboards, repository has %d", len(files), len(committed))
	}
	for _, f := range files {
		want, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("dashboards", filepath.Base(f)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("dashboards/%s is stale: run go run dashboards/gen.go dashboards", filepath.Base(f))
		}
	}
}
