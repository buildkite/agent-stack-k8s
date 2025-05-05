// The generate_metrics_doc tool can be used to generate some markdown tables
// from the internal Prometheus metrics registry.

// Example usage:
//
//	go generate ./docs
package main

//go:generate go run generate_metrics_doc.go

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"reflect"
	"slices"
	"strings"
	"text/template"

	_ "github.com/buildkite/agent-stack-k8s/v2/internal/controller/deduper"
	_ "github.com/buildkite/agent-stack-k8s/v2/internal/controller/limiter"
	_ "github.com/buildkite/agent-stack-k8s/v2/internal/controller/monitor"
	_ "github.com/buildkite/agent-stack-k8s/v2/internal/controller/scheduler"
	"github.com/prometheus/client_golang/prometheus"
)

func labels(constLabelPairs, variableLabels reflect.Value) []string {
	var labels []string
	for i := range constLabelPairs.Len() {
		labels = append(labels, "`"+constLabelPairs.Index(i).Elem().FieldByName("Name").Elem().String()+"`")
	}
	for i := range variableLabels.Len() {
		labels = append(labels, "`"+variableLabels.Index(i).String()+"`")
	}
	return labels
}

func main() {
	components := map[string][]string{
		"completion_watcher": nil,
		"deduper":            nil,
		"job_watcher":        nil,
		"limiter":            nil,
		"monitor":            nil,
		"pod_watcher":        nil,
		"scheduler":          nil,
	}
	var other []string

	descCh := make(chan *prometheus.Desc)
	go func() {
		prometheus.DefaultRegisterer.(*prometheus.Registry).Describe(descCh)
		close(descCh)
	}()

	for desc := range descCh {
		// Of course they make prometheus.Desc full of unexported fields.
		dv := reflect.ValueOf(desc).Elem()
		name := dv.FieldByName("fqName").String()
		help := dv.FieldByName("help").String()
		clp := dv.FieldByName("constLabelPairs")                            // []*dto.LabelPair
		vln := dv.FieldByName("variableLabels").Elem().FieldByName("names") // []string
		labels := strings.Join(labels(clp, vln), ", ")
		if labels == "" {
			labels = "-"
		}

		if !strings.HasPrefix(name, "buildkite_") {
			continue
		}
		ungrouped := true
		for c := range components {
			if strings.HasPrefix(name, "buildkite_"+c) {
				ungrouped = false
				components[c] = append(components[c], fmt.Sprintf("`%s` | %s | %s", name, labels, help))
				break
			}
		}
		if ungrouped {
			other = append(other, fmt.Sprintf("`%s` | %s | %s", name, labels, help))
		}
	}

	for c := range components {
		slices.Sort(components[c])
	}
	slices.Sort(other)

	tmpl := template.Must(template.New("markdown").Parse(`{{range $key, $val := .Grouped}}
## {{$key}}

Full metric name | Labels | Description
--- | --- | ---
{{range $val}}{{.}}
{{end}}{{end}}
## Other

Full metric name | Labels | Description
--- | --- | ---
{{range .Ungrouped}}{{.}}
{{end}}
`))

	// Now interpolate it into the doc
	oldFile, err := os.Open("prometheus_metrics.md")
	if err != nil {
		log.Fatalf("Couldn't open file: %v", err)
	}
	defer oldFile.Close()

	newFile, err := os.CreateTemp(".", "prometheus_metrics.md.*")
	if err != nil {
		log.Fatalf("Couldn't create temp file: %v", err)
	}
	defer func() {
		newFile.Close()
		os.Remove(newFile.Name())
	}()

	interpolating := false
	sc := bufio.NewScanner(oldFile)
	for sc.Scan() {
		line := sc.Text()
		switch strings.TrimSpace(line) {
		case "<!--START generate_metrics_doc.go-->":
			interpolating = true
			fmt.Fprintln(newFile, line)
			if err := tmpl.Execute(newFile, map[string]any{
				"Grouped":   components,
				"Ungrouped": other,
			}); err != nil {
				log.Printf("Couldn't execute template: %v", err)
				return
			}

		case "<!--END generate_metrics_doc.go-->":
			interpolating = false
		}

		if !interpolating {
			fmt.Fprintln(newFile, line)
		}
	}

	if err := oldFile.Close(); err != nil {
		log.Printf("Couldn't close old file: %v", err)
		return
	}
	if err := newFile.Close(); err != nil {
		log.Printf("Couldn't close new file: %v", err)
		return
	}
	if err := os.Rename(newFile.Name(), oldFile.Name()); err != nil {
		log.Printf("Couldn't rename new file over old file: %v", err)
	}
}
