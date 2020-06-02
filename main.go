package main

// TODO - better error handling (remove all panics)
// TODO - help document explaining template usage

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
	"io/ioutil"
	"log"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

var stderr = log.New(os.Stderr, "", 1)

var ValidActions = []string{
	"VarsTable",
	"OutputsTable",
	"ManagedResourcesTable",
	"DataSourcesTable",
	"RenderTemplate",
}

type CliOpts struct {
	TfPath       string
	Action       string
	TemplatePath string
	RepoUrl      string
	ModulePath   string
}

type TemplateData struct {
	TerraformVarsTable             string
	TerraformOutputsTable          string
	TerraformManagedResourcesTable string
	TerraformDataSourcesTable      string
	TerraformModulesTable          string
	MarkdownTOC                    string
	RepoBaseUrl                    string
}

type TfTableObject struct {
	Name, Type, Description, Location string
}

func StringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func CheckErr(e error, msg string) {
	if e != nil {
		if msg != "" {
			stderr.Println(msg)
		}
		stderr.Println(e.Error())
		os.Exit(1)
	}
}

var (
	rHashHeader        = regexp.MustCompile("^(?P<indent>#+) ?(?P<title>.+)$")
	rUnderscoreHeader1 = regexp.MustCompile("^=+$")
	rUnderscoreHeader2 = regexp.MustCompile("^\\-+$")
)

//  Slugufy came from: https://github.com/sebdah/markdown-toc/tree/master/toc
func slugify(s string) string {
	droppedChars := []string{
		"\"", "'", "`", ".",
		"!", ",", "~", "&",
		"%", "^", "*", "#",
		"@", "|",
		"(", ")",
		"{", "}",
		"[", "]",
	}

	s = strings.ToLower(s)
	for _, c := range droppedChars {
		s = strings.Replace(s, c, "", -1)
	}

	s = strings.Replace(s, " ", "-", -1)

	return s
}

//  https://github.com/sebdah/markdown-toc/tree/master/toc
func BuildMarkdownToc(d []byte, depth, skipHeaders int) ([]string, error) {
	toc := []string{
		"Table of Contents",
		"=================",
	}

	seenHeaders := make(map[string]int)
	var previousLine string
	appendToC := func(title string, indent int) {
		link := slugify(title)
		if skipHeaders > 0 {
			skipHeaders--
			return
		}

		if _, ok := seenHeaders[link]; ok {
			seenHeaders[link]++
			link = fmt.Sprintf("%s-%d", link, seenHeaders[link]-1)
		} else {
			seenHeaders[link] = 1
		}
		toc = append(toc, fmt.Sprintf("%s1. [%s](#%s)", strings.Repeat("   ", indent), title, link))
	}

	s := bufio.NewScanner(bytes.NewReader(d))
	for s.Scan() {
		switch {
		case rHashHeader.Match(s.Bytes()):
			m := rHashHeader.FindStringSubmatch(s.Text())
			if depth > 0 && len(m[1]) > depth {
				continue
			}
			appendToC(m[2], len(m[1])-1)

		case rUnderscoreHeader1.Match(s.Bytes()):
			appendToC(previousLine, 0)

		case rUnderscoreHeader2.Match(s.Bytes()):
			if depth > 0 && depth < 2 {
				continue
			}
			appendToC(previousLine, 1)
		}
		previousLine = s.Text()
	}
	if err := s.Err(); err != nil {
		return []string{}, err
	}

	return toc, nil
}

func ParseCli() *CliOpts {
	opts := CliOpts{}
	tfPathPtr := flag.String("path", "", "The path to the Terraform Module to inspect.")
	actionPtr := flag.String("action", "", fmt.Sprintf("The Action to perform. %s", ValidActions))
	templatePathPtr := flag.String("templatePath", "", "The path to the template to render")
	repoUrlPtr := flag.String("repoUrl", "", "The URL path used as a prefix for links")
	modulePathPtr := flag.String("modulePath", "", "The path of the module relative to the repository")
	flag.Parse()
	opts.TfPath = *tfPathPtr
	opts.Action = *actionPtr
	opts.TemplatePath = *templatePathPtr
	opts.RepoUrl = *repoUrlPtr
	opts.ModulePath = *modulePathPtr

	if opts.TfPath == "" {
		flag.Usage()
		panic("no TF Path set")
	}
	if opts.Action == "" {
		flag.Usage()
		panic("No Action set")
	}
	if !StringInSlice(opts.Action, ValidActions) {
		panic(fmt.Sprintf("Action %s is not one of: %s", opts.Action, ValidActions))
	}
	if opts.Action == "RenderTemplate" && opts.TemplatePath == "" {
		CheckErr(errors.New("no Template path specified"), "")
	}

	return &opts
}

func MarkdownTableCellEscape(cellText string) string {
	// Replace Newlines with <br/>
	re := regexp.MustCompile(`\r?\n`)
	cellText = re.ReplaceAllString(cellText, "<br>")
	return cellText
}

func MarkdownTable(headings []string, lengths []string, data [][]string) string {
	// TODO - input/parameter validation
	table := "|"
	for _, h := range headings {
		table += fmt.Sprintf(" %s |", h)
	}
	table += "\n|"
	for _, l := range lengths {
		table += fmt.Sprintf(" %s |", l)
	}

	for _, d := range data {
		table += "\n|"
		for _, val := range d {
			table += fmt.Sprintf(" %s |", MarkdownTableCellEscape(val))
		}
	}

	return table

}

func getSortedKeys(objs map[string]TfTableObject) []string {
	keys := make([]string, 0, len(objs))
	for k := range objs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func GetVarsTable(module *tfconfig.Module, baseUrl, modulePath string) string {
	headings := []string{"Variable", "Type", "Description", "Code Position"}
	lengths := []string{"----", "------", "--------", "------"}
	data := [][]string{}

	// Make a map of item objects
	var objs = make(map[string]TfTableObject)
	for _, item := range module.Variables {
		tfpathbits := strings.Split(item.Pos.Filename, "/")
		tffile := tfpathbits[len(tfpathbits)-1]
		objs[item.Name] = TfTableObject{
			Name:        item.Name,
			Type:        item.Type,
			Description: item.Description,
			Location:    fmt.Sprintf("[%s: %d](%s/%s/%s#L%d)", tffile, item.Pos.Line, baseUrl, modulePath, tffile, item.Pos.Line),
		}
	}
	for _, k := range getSortedKeys(objs) {
		data = append(data, []string{objs[k].Name, objs[k].Type, objs[k].Description, objs[k].Location})
	}

	return MarkdownTable(headings, lengths, data)
}

func GetOutputsTable(module *tfconfig.Module, baseUrl, modulePath string) string {
	headings := []string{"Output name", "Description", "Code Position"}
	lengths := []string{"----", "--------", "------"}
	data := [][]string{}

	var objs = make(map[string]TfTableObject) // Make a map of output objects
	for _, item := range module.Outputs {
		tfpathbits := strings.Split(item.Pos.Filename, "/")
		tffile := tfpathbits[len(tfpathbits)-1]
		objs[item.Name] = TfTableObject{
			Name:        item.Name,
			Type:        "",
			Description: item.Description,
			Location:    fmt.Sprintf("[%s: %d](%s/%s/%s#L%d)", tffile, item.Pos.Line, baseUrl, modulePath, tffile, item.Pos.Line),
		}
	}

	for _, k := range getSortedKeys(objs) {
		data = append(data, []string{objs[k].Name, objs[k].Description, objs[k].Location})
	}
	return MarkdownTable(headings, lengths, data)
}

func GetManagedResourcesTable(module *tfconfig.Module, baseUrl, modulePath string) string {
	headings := []string{"Resource Name", "Resource Type", "Code Position"}
	lengths := []string{"----", "--------", "------"}
	data := [][]string{}

	var objs = make(map[string]TfTableObject) // Make a map of output objects
	for _, item := range module.ManagedResources {
		tfpathbits := strings.Split(item.Pos.Filename, "/")
		tffile := tfpathbits[len(tfpathbits)-1]
		objs[item.Name] = TfTableObject{
			Name:        item.Name,
			Type:        item.Type,
			Description: "",
			Location:    fmt.Sprintf("[%s: %d](%s/%s/%s#L%d)", tffile, item.Pos.Line, baseUrl, modulePath, tffile, item.Pos.Line),
		}
	}
	for _, k := range getSortedKeys(objs) {
		data = append(data, []string{objs[k].Name, objs[k].Type, objs[k].Location})
	}
	return MarkdownTable(headings, lengths, data)
}

func GetDataSourcesTable(module *tfconfig.Module, baseUrl, modulePath string) string {
	headings := []string{"Resource Name", "Resource Type", "Code Position"}
	lengths := []string{"----", "--------", "------"}
	data := [][]string{}

	var objs = make(map[string]TfTableObject) // Make a map of output objects
	for _, item := range module.DataResources {
		tfpathbits := strings.Split(item.Pos.Filename, "/")
		tffile := tfpathbits[len(tfpathbits)-1]
		objs[item.Name] = TfTableObject{
			Name:        item.Name,
			Type:        item.Type,
			Description: "",
			Location:    fmt.Sprintf("[%s: %d](%s/%s/%s#L%d)", tffile, item.Pos.Line, baseUrl, modulePath, tffile, item.Pos.Line),
		}
	}
	for _, k := range getSortedKeys(objs) {
		data = append(data, []string{objs[k].Name, objs[k].Type, objs[k].Location})
	}
	return MarkdownTable(headings, lengths, data)
}

func GetModulesTable(module *tfconfig.Module, baseUrl, modulePath string) string {
	headings := []string{"Module Name", "Module Source", "Module Location"}
	lengths := []string{"----", "--------", "------"}
	data := [][]string{}

	var objs = make(map[string]TfTableObject) // Make a map of output objects
	for _, item := range module.ModuleCalls {
		tfpathbits := strings.Split(item.Pos.Filename, "/")
		tffile := tfpathbits[len(tfpathbits)-1]
		objs[item.Name] = TfTableObject{
			Name:        item.Name,
			Type:        item.Source,
			Description: item.Version,
			Location:    fmt.Sprintf("[%s: %d](%s/%s/%s#L%d)", tffile, item.Pos.Line, baseUrl, modulePath, tffile, item.Pos.Line),
		}
	}
	for _, k := range getSortedKeys(objs) {
		data = append(data, []string{objs[k].Name, objs[k].Type, objs[k].Location})
	}
	return MarkdownTable(headings, lengths, data)
}

func main() {

	cliOpts := ParseCli()

	module, diags := tfconfig.LoadModule(cliOpts.TfPath)
	//baseUrl := GitLabBaseUrl(cliOpts.TfPath)

	if diags.HasErrors() {
		panic("Problem Loading Module: " + diags.Error())
	}

	if cliOpts.Action == "VarsTable" {
		fmt.Println(GetVarsTable(module, cliOpts.RepoUrl, cliOpts.ModulePath))
	} else if cliOpts.Action == "OutputsTable" {
		fmt.Println(GetOutputsTable(module, cliOpts.RepoUrl, cliOpts.ModulePath))
	} else if cliOpts.Action == "ManagedResourcesTable" {
		fmt.Println(GetManagedResourcesTable(module, cliOpts.RepoUrl, cliOpts.ModulePath))
	} else if cliOpts.Action == "RenderTemplate" {

		// Load the template
		name := path.Base(cliOpts.TemplatePath)
		t, err := template.New(name).Funcs(template.FuncMap{
			"rawfile": func(filepath string) (string, error) {
				parent := path.Dir(cliOpts.TemplatePath)
				rawFilePath := parent + "/" + filepath
				fileBytes, err := ioutil.ReadFile(rawFilePath)

				return string(fileBytes), err
			},
		}).ParseFiles(cliOpts.TemplatePath)
		CheckErr(err, fmt.Sprintf("Problem loading template: %s", cliOpts.TemplatePath))

		readmeTemplateBytes, err := ioutil.ReadFile(cliOpts.TemplatePath)
		CheckErr(err, "Failed to read template: %s")
		toc, err := BuildMarkdownToc(readmeTemplateBytes, 3, 0)

		data := TemplateData{
			TerraformOutputsTable:          GetOutputsTable(module, cliOpts.RepoUrl, cliOpts.ModulePath),
			TerraformVarsTable:             GetVarsTable(module, cliOpts.RepoUrl, cliOpts.ModulePath),
			TerraformManagedResourcesTable: GetManagedResourcesTable(module, cliOpts.RepoUrl, cliOpts.ModulePath),
			TerraformDataSourcesTable:      GetDataSourcesTable(module, cliOpts.RepoUrl, cliOpts.ModulePath),
			TerraformModulesTable:          GetModulesTable(module, cliOpts.RepoUrl, cliOpts.ModulePath),
			MarkdownTOC:                    strings.Join(toc, "\n"),
			RepoBaseUrl:                    cliOpts.RepoUrl,
		}
		CheckErr(t.Execute(os.Stdout, data), fmt.Sprintf("failed rendering template: %s", cliOpts.TemplatePath))

	} else {
		CheckErr(errors.New(fmt.Sprintf("Action %s not implented yet", cliOpts.Action)), "")

	}

}
