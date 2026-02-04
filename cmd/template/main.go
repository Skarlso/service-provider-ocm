package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/imports"
)

// exec from project root
const templatesDir = "cmd/template/files"

type TemplateData struct {
	Group               string
	Version             string
	Kind                string
	KindLower           string
	Module              string
	RepoName            string
	WithExample         bool
	WithWorkloadCluster bool
}

//nolint:gocyclo
func main() {
	group := flag.String("group", "foo", "GVK group prefix (will always be suffixed with services.openmcp.cloud)")
	kind := flag.String("kind", "FooService", "GVK kind")
	withExample := flag.Bool("v", false, "Generate with sample code")
	withWorkloadCluster := flag.Bool("w", false, "Reconcile with workload cluster")
	module := flag.String("module", "github.com/openmcp-project/service-provider-template", "Go module")
	flag.Parse()
	data := TemplateData{
		Group:               *group,
		Kind:                *kind,
		KindLower:           strings.ToLower(*kind),
		Module:              *module,
		RepoName:            filepath.Base(*module),
		WithExample:         *withExample,
		WithWorkloadCluster: *withWorkloadCluster,
	}
	// directories
	apiDir := filepath.Join("api", "v1alpha1")
	crdDir := filepath.Join("api", "crds", "manifests")
	cmdDir := filepath.Join("cmd", data.RepoName)
	controllerDir := filepath.Join("internal", "controller")
	e2eDir := filepath.Join("test", "e2e")

	if cmdDir != "cmd/service-provider-template" {
		err := os.Rename("cmd/service-provider-template", cmdDir)
		if err != nil {
			log.Fatalf("failed to rename directory: %v", err)
		}
	}

	// files
	providercrdFile := filepath.Join(crdDir, fmt.Sprintf("%s.services.openmcp.cloud_%ss.yaml", *group, data.KindLower))
	configcrdFile := filepath.Join(crdDir, fmt.Sprintf("%s.services.openmcp.cloud_providerconfigs.yaml", *group))
	typesFile := filepath.Join(apiDir, fmt.Sprintf("%s_types.go", data.KindLower))
	groupVersionFile := filepath.Join(apiDir, "groupversion_info.go")
	mainFile := filepath.Join(cmdDir, "main.go")
	mainTestFile := filepath.Join(e2eDir, "main_test.go")
	taskfileFile := "Taskfile.yaml"
	controllerFile := filepath.Join(controllerDir, fmt.Sprintf("%s_controller.go", data.KindLower))
	testFile := filepath.Join(e2eDir, fmt.Sprintf("%s_test.go", data.KindLower))
	testOnboardingFile := filepath.Join(e2eDir, "onboarding", fmt.Sprintf("%s.yaml", data.KindLower))
	testPlatformFile := filepath.Join(e2eDir, "platform", "providerconfig.yaml")
	// api
	execTemplate("api_crd_serviceproviderapi.yaml.tmpl", providercrdFile, data)
	execTemplate("api_crd_providerconfig.yaml.tmpl", configcrdFile, data)
	execTemplate("api_types.go.tmpl", typesFile, data)
	execTemplate("api_groupversion_info.go.tmpl", groupVersionFile, data)
	// cmd
	execTemplate("main.go.tmpl", mainFile, data)
	// controller
	execTemplate("controller.go.tmpl", controllerFile, data)
	// e2e tests
	execTemplate("main_test.go.tmpl", mainTestFile, data)
	execTemplate("test.go.tmpl", testFile, data)
	execTemplate("testdata_providerconfig.yaml.tmpl", testPlatformFile, data)
	execTemplate("testdata_service.yaml.tmpl", testOnboardingFile, data)
	// root
	execTemplate("Taskfile.yaml.tmpl", taskfileFile, data)

	// rename module
	if err := exec.Command("go", "mod", "edit", "-module", *module).Run(); err != nil {
		log.Fatalf("go mod edit failed: %v", err)
	}
	// replace module in imports and remove redundant imports
	rootDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("could not determine current working directory: %v", err)
	}
	err = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			if err := replaceImports(path, "github.com/openmcp-project/service-provider-template", *module); err != nil {
				return err
			}
			if err := fixImports(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("rename imports failed: %v", err)
	}
	// clean up repo (remove /cmd/template)
	err = os.RemoveAll("cmd/template")
	if err != nil {
		log.Fatalf("Error removing template directory: %v", err)
		return
	}
	fmt.Printf("Generated service-provider for %s/%s' in %s\n", data.Group, data.Kind, *module)
}

//nolint:gocritic
func execTemplate(templateName, outPath string, data TemplateData) {
	tplPath := filepath.Join(templatesDir, templateName)
	tpl, err := template.ParseFiles(tplPath)
	if err != nil {
		log.Fatalf("failed parsing template %s: %v", templateName, err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		log.Fatalf("failed creating file %s: %v", outPath, err)
	}
	log.Default().Println(outPath)
	defer closeFile(outPath, f)
	if err := tpl.Execute(f, data); err != nil {
		log.Fatalf("failed executing template %s: %v", templateName, err)
	}
}

func replaceImports(filename, oldRepo, newRepo string) error {
	input, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer closeFile(filename, input)
	var result []string
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.Replace(line, oldRepo, newRepo, 1)
		result = append(result, line)
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	// keep empty line
	result = append(result, "")
	return os.WriteFile(filename, []byte(strings.Join(result, "\n")), 0644)
}

func fixImports(filename string) error {
	data, err := imports.Process(filename, nil, nil)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func closeFile(filename string, c io.Closer) {
	if err := c.Close(); err != nil {
		log.Fatalf("please reset/checkout %s and try again: %v", filename, err)
	}
}
