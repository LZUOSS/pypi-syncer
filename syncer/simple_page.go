package syncer

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const pythonHostedPrefix = "https://files.pythonhosted.org/packages/"
const pythonHostedPrefixNoSlash = "https://files.pythonhosted.org/packages"

// rewriteURL replaces upstream file URLs with local prefix-based URLs.
func rewriteURL(fileURL, prefix string) string {
	if strings.HasPrefix(fileURL, pythonHostedPrefix) {
		return prefix + "/packages/" + strings.TrimPrefix(fileURL, pythonHostedPrefix)
	}
	if strings.HasPrefix(fileURL, pythonHostedPrefixNoSlash) {
		return prefix + "/packages" + strings.TrimPrefix(fileURL, pythonHostedPrefixNoSlash)
	}
	return fileURL
}

// GenerateSimplePages creates the simple index pages for a package.
func GenerateSimplePages(repoPath, packageName string, files []FileInfo, prefix string) error {
	normalized := normalizeName(packageName)
	dir := filepath.Join(repoPath, "simple", normalized)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Generate index.html (PEP 503)
	if err := writeSimpleHTML(filepath.Join(dir, "index.html"), packageName, files, prefix, false); err != nil {
		return err
	}

	// Generate index.v1_html (PEP 691)
	if err := writeSimpleHTML(filepath.Join(dir, "index.v1_html"), packageName, files, prefix, true); err != nil {
		return err
	}

	// Generate index.v1_json (PEP 691)
	if err := writeSimpleJSON(filepath.Join(dir, "index.v1_json"), normalized, files, prefix); err != nil {
		return err
	}

	return nil
}

func writeSimpleHTML(path, packageName string, files []FileInfo, prefix string, pep691 bool) error {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n  <head>\n")
	b.WriteString(fmt.Sprintf("    <title>Links for %s</title>\n", html.EscapeString(packageName)))
	if pep691 {
		b.WriteString("    <meta name=\"pypi:repository-version\" content=\"1.1\"/>\n")
	}
	b.WriteString("  </head>\n  <body>\n")
	b.WriteString(fmt.Sprintf("    <h1>Links for %s</h1>\n", html.EscapeString(packageName)))

	for _, f := range files {
		url := rewriteURL(f.URL, prefix)
		href := url
		if f.SHA256 != "" {
			href += "#sha256=" + f.SHA256
		}
		yankedAttr := ""
		if f.Yanked {
			yankedAttr = ` data-yanked=""`
		}
		requiresPython := ""
		if f.RequiresPython != "" {
			requiresPython = fmt.Sprintf(` data-requires-python="%s"`, html.EscapeString(f.RequiresPython))
		}
		b.WriteString(fmt.Sprintf("    <a href=\"%s\"%s%s>%s</a><br/>\n",
			href, requiresPython, yankedAttr, html.EscapeString(f.Filename)))
	}

	b.WriteString("  </body>\n</html>\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeSimpleJSON(path, normalizedName string, files []FileInfo, prefix string) error {
	type fileHash struct {
		SHA256 string `json:"sha256,omitempty"`
		MD5    string `json:"md5,omitempty"`
	}
	type jsonFile struct {
		Filename       string   `json:"filename"`
		URL            string   `json:"url"`
		Hashes         fileHash `json:"hashes"`
		RequiresPython string   `json:"requires-python,omitempty"`
		Yanked         bool     `json:"yanked"`
	}
	type jsonResponse struct {
		Meta  map[string]string `json:"meta"`
		Name  string            `json:"name"`
		Files []jsonFile        `json:"files"`
	}

	jFiles := make([]jsonFile, 0, len(files))
	for _, f := range files {
		jFiles = append(jFiles, jsonFile{
			Filename:       f.Filename,
			URL:            rewriteURL(f.URL, prefix),
			Hashes:         fileHash{SHA256: f.SHA256, MD5: f.MD5},
			RequiresPython: f.RequiresPython,
			Yanked:         f.Yanked,
		})
	}

	resp := jsonResponse{
		Meta:  map[string]string{"api-version": "1.1"},
		Name:  normalizedName,
		Files: jFiles,
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// GenerateRootSimplePages creates the root simple index listing all packages.
func GenerateRootSimplePages(repoPath string, packageNames []string, prefix string) error {
	dir := filepath.Join(repoPath, "simple")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	sorted := make([]string, len(packageNames))
	copy(sorted, packageNames)
	sort.Strings(sorted)

	// index.html
	if err := writeRootHTML(filepath.Join(dir, "index.html"), sorted, prefix, false); err != nil {
		return err
	}

	// index.v1_html
	if err := writeRootHTML(filepath.Join(dir, "index.v1_html"), sorted, prefix, true); err != nil {
		return err
	}

	// index.v1_json
	if err := writeRootJSON(filepath.Join(dir, "index.v1_json"), sorted); err != nil {
		return err
	}

	return nil
}

func writeRootHTML(path string, packageNames []string, prefix string, pep691 bool) error {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n  <head>\n")
	b.WriteString("    <title>Simple Index</title>\n")
	if pep691 {
		b.WriteString("    <meta name=\"pypi:repository-version\" content=\"1.1\"/>\n")
	}
	b.WriteString("  </head>\n  <body>\n")

	for _, name := range packageNames {
		normalized := normalizeName(name)
		b.WriteString(fmt.Sprintf("    <a href=\"%s/simple/%s/\">%s</a><br/>\n",
			prefix, normalized, html.EscapeString(name)))
	}

	b.WriteString("  </body>\n</html>\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeRootJSON(path string, packageNames []string) error {
	type project struct {
		Name string `json:"name"`
	}
	type jsonResponse struct {
		Meta     map[string]string `json:"meta"`
		Projects []project         `json:"projects"`
	}

	projects := make([]project, 0, len(packageNames))
	for _, name := range packageNames {
		projects = append(projects, project{Name: name})
	}

	resp := jsonResponse{
		Meta:     map[string]string{"api-version": "1.1"},
		Projects: projects,
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
