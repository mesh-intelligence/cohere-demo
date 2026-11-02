// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mesh-intelligence/declarative-agents/agent-core/pkg/appmanifest"
	"gopkg.in/yaml.v3"
)

const (
	// chatbotApplicationName is the application identity in
	// chatbotCompositionManifest. Staged packages record it, so the manifest,
	// the provenance contract, and the tests read it from here.
	chatbotApplicationName     = "cohere-demo"
	chatbotCompositionManifest = "agents/application.yaml"
	chatbotClosureProvenance   = "provenance/application-closure.yaml"
)

// chatbotTierVariantSources are the tier-selected files staged beside the
// closure (see chatbotPackageProvenance.TierVariants). Paths are
// application-root relative; each stages under profiles/ at the same path.
var chatbotTierVariantSources = []string{
	"agents/chatbot/request-machine-ollama.yaml",
	"agents/chatbot/request-tools-ollama.yaml",
}

var chartProfileReferenceRE = regexp.MustCompile(`agents/[a-z0-9-]+/profile\.yaml`)

type chatbotComposition struct {
	manifest  appmanifest.Manifest
	inventory appmanifest.Inventory
}

type chatbotPackageFile struct {
	Source      string   `yaml:"source"`
	RuntimePath string   `yaml:"runtime_path"`
	PackagePath string   `yaml:"package_path"`
	Checksum    string   `yaml:"checksum"`
	Roots       []string `yaml:"roots"`
}

type chatbotPackageProvenance struct {
	SchemaVersion       int                   `yaml:"schema_version"`
	Application         string                `yaml:"application"`
	CompositionManifest string                `yaml:"composition_manifest"`
	ManifestChecksum    string                `yaml:"manifest_checksum"`
	MountPath           string                `yaml:"mount_path"`
	Sources             chatbotPackageSources `yaml:"sources"`
	Files               []chatbotPackageFile  `yaml:"files"`
	// TierVariants are staged beside the closure rather than through it: the
	// pre-provider request machine and its chat-LLM vocabulary belong to no
	// profile's closure, because a request profile is not valid as a closure
	// root (its actions are registered by the machine_request binding that
	// loads it, not by its own tools selection). The ConfigMap projects one of
	// the two turns under the canonical request-machine.yaml key by tier
	// (srd009 R6.3, GH-224). Checksummed here so the staged-package validation
	// covers them exactly as it covers the closure.
	TierVariants []chatbotPackageFile  `yaml:"tier_variants,omitempty"`
	Closure      appmanifest.Inventory `yaml:"closure"`
}

type chatbotPackageSources struct {
	Application chatbotCheckoutProvenance `yaml:"application"`
	Catalog     chatbotCheckoutProvenance `yaml:"catalog"`
}

type chatbotCheckoutProvenance struct {
	Available bool   `yaml:"available"`
	Revision  string `yaml:"revision,omitempty"`
	Dirty     bool   `yaml:"dirty,omitempty"`
}

// resolveChatbotComposition loads and resolves the composition manifest from
// the application root as given. The identity comes from the manifest's
// application field rather than from the root directory's base name (upstream
// GH-1881, pinned at v0.20260827.2), so a gh-* worktree resolves exactly as a
// checkout named for the application does and no root has to be staged to be
// read. The inventory carries ownership-prefixed logical sources rather than
// absolute paths, so the closure the caller stages from records no checkout
// location.
func resolveChatbotComposition(applicationRoot, catalogRoot string) (chatbotComposition, error) {
	root, err := filepath.Abs(filepath.Clean(applicationRoot))
	if err != nil {
		return chatbotComposition{}, fmt.Errorf("resolve application root %s: %w", applicationRoot, err)
	}
	options := appmanifest.Options{
		ApplicationRoot: root,
		CatalogRoot:     catalogRoot,
	}
	manifest, err := appmanifest.Load(
		filepath.Join(root, filepath.FromSlash(chatbotCompositionManifest)),
		options,
	)
	if err != nil {
		return chatbotComposition{}, err
	}
	inventory, err := appmanifest.Resolve(manifest, options)
	if err != nil {
		return chatbotComposition{}, fmt.Errorf("resolve %s composition: %w", chatbotApplicationName, err)
	}
	return chatbotComposition{manifest: manifest, inventory: inventory}, nil
}

// stageChatbotComposition copies exactly the shared resolver's transitive
// inventory into the chart. Runtime paths below agents/ are projected through
// the manifest's /profiles mount; explicitly external UI paths (collector-ui)
// remain chart-relative and are mounted by their owning workload.
func stageChatbotComposition(chartRoot, applicationRoot, catalogRoot string) error {
	composition, err := resolveChatbotComposition(applicationRoot, catalogRoot)
	if err != nil {
		return err
	}
	if err := validateChartProfileReferences(chartRoot, composition.manifest); err != nil {
		return err
	}
	files := make([]chatbotPackageFile, 0, len(composition.inventory.Files))
	for _, file := range composition.inventory.Files {
		packagePath, err := chatbotPackagePath(
			composition.manifest, file.RuntimePath, file.PackagePath)
		if err != nil {
			return err
		}
		source, err := chatbotInventorySource(applicationRoot, catalogRoot, file.Source)
		if err != nil {
			return err
		}
		destination := filepath.Join(chartRoot, filepath.FromSlash(packagePath))
		if err := copyChatbotInventoryFile(source, destination, file.Checksum); err != nil {
			return fmt.Errorf("stage manifest closure %s: %w", file.RuntimePath, err)
		}
		files = append(files, chatbotPackageFile{
			Source: file.Source, RuntimePath: file.RuntimePath, PackagePath: packagePath,
			Checksum: file.Checksum, Roots: append([]string(nil), file.Roots...),
		})
	}
	variants := make([]chatbotPackageFile, 0, len(chatbotTierVariantSources))
	for _, relative := range chatbotTierVariantSources {
		source := filepath.Join(applicationRoot, filepath.FromSlash(relative))
		data, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("stage tier variant %s: %w", relative, err)
		}
		sum := sha256.Sum256(data)
		checksum := fmt.Sprintf("sha256:%x", sum)
		packagePath := "profiles/" + relative
		destination := filepath.Join(chartRoot, filepath.FromSlash(packagePath))
		if err := copyChatbotInventoryFile(source, destination, checksum); err != nil {
			return fmt.Errorf("stage tier variant %s: %w", relative, err)
		}
		variants = append(variants, chatbotPackageFile{
			Source: "local:" + relative, RuntimePath: relative, PackagePath: packagePath,
			Checksum: checksum,
		})
	}
	manifestData, err := os.ReadFile(filepath.Join(
		applicationRoot, filepath.FromSlash(chatbotCompositionManifest)))
	if err != nil {
		return err
	}
	manifestSum := sha256.Sum256(manifestData)
	provenance := chatbotPackageProvenance{
		SchemaVersion:       1,
		Application:         composition.manifest.Application,
		CompositionManifest: chatbotCompositionManifest,
		ManifestChecksum:    fmt.Sprintf("sha256:%x", manifestSum),
		MountPath:           composition.manifest.Runtime.MountPath,
		Sources: chatbotPackageSources{
			Application: chatbotSourceRevision(applicationRoot),
			Catalog:     chatbotCatalogRevision(applicationRoot, catalogRoot),
		},
		Files:        files,
		TierVariants: variants,
		Closure:      composition.inventory,
	}
	data, err := yaml.Marshal(provenance)
	if err != nil {
		return fmt.Errorf("marshal chatbot package provenance: %w", err)
	}
	destination := filepath.Join(chartRoot, filepath.FromSlash(chatbotClosureProvenance))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(destination, data, 0o644); err != nil {
		return fmt.Errorf("write chatbot package provenance: %w", err)
	}
	return validateStagedChatbotComposition(chartRoot, provenance)
}

// chatbotCatalogRevision records which catalog content the staged package was
// built from. The catalog resolves to a git checkout under go.work and to the
// read-only module cache under the pin; the cache carries no git metadata, so
// the pinned pseudo-version supplies the revision there. It names the same
// commit, which is what the provenance is for (GH-110).
func chatbotCatalogRevision(applicationRoot, catalogRoot string) chatbotCheckoutProvenance {
	if provenance := chatbotSourceRevision(catalogRoot); provenance.Available {
		return provenance
	}
	revision := pseudoVersionRevision(pinnedModuleVersion(applicationRoot, catalogModule))
	if revision == "" {
		return chatbotCheckoutProvenance{}
	}
	return chatbotCheckoutProvenance{Available: true, Revision: revision}
}

func chatbotSourceRevision(root string) chatbotCheckoutProvenance {
	revision, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return chatbotCheckoutProvenance{}
	}
	status, err := exec.Command(
		"git", "-C", root, "status", "--porcelain", "--untracked-files=normal", "--", ".",
	).Output()
	if err != nil {
		return chatbotCheckoutProvenance{}
	}
	return chatbotCheckoutProvenance{
		Available: true,
		Revision:  strings.TrimSpace(string(revision)),
		Dirty:     len(bytes.TrimSpace(status)) > 0,
	}
}

func chatbotPackagePath(manifest appmanifest.Manifest, runtimePath, declaredPackagePath string) (string, error) {
	runtimePath = path.Clean(filepath.ToSlash(runtimePath))
	if runtimePath == "." || runtimePath == ".." || strings.HasPrefix(runtimePath, "../") ||
		path.IsAbs(runtimePath) {
		return "", fmt.Errorf("invalid closure runtime path %q", runtimePath)
	}
	declaredPackagePath = path.Clean(filepath.ToSlash(declaredPackagePath))
	if declaredPackagePath != "" && declaredPackagePath != "." && declaredPackagePath != runtimePath {
		if declaredPackagePath == ".." || strings.HasPrefix(declaredPackagePath, "../") ||
			path.IsAbs(declaredPackagePath) {
			return "", fmt.Errorf("invalid closure package path %q", declaredPackagePath)
		}
		return declaredPackagePath, nil
	}
	if strings.HasPrefix(runtimePath, "agents/") ||
		strings.HasPrefix(runtimePath, "applications/") {
		mountRoot := strings.TrimPrefix(path.Clean(manifest.Runtime.MountPath), "/")
		if mountRoot == "" || mountRoot == "." {
			return "", fmt.Errorf("manifest mount path %q has no package root", manifest.Runtime.MountPath)
		}
		return path.Join(mountRoot, runtimePath), nil
	}
	return runtimePath, nil
}

func chatbotInventorySource(applicationRoot, catalogRoot, logical string) (string, error) {
	for prefix, root := range map[string]string{
		"application/": applicationRoot,
		"catalog/":     catalogRoot,
	} {
		if strings.HasPrefix(logical, prefix) {
			return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(logical, prefix))), nil
		}
	}
	return "", fmt.Errorf("inventory source %q has no ownership prefix", logical)
}

func copyChatbotInventoryFile(source, destination, checksum string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source %s is not a regular file", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	actual := fmt.Sprintf("sha256:%x", sum)
	if actual != checksum {
		return fmt.Errorf("source checksum changed: inventory %s, content %s", checksum, actual)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	// The staged file keeps the source's permissions plus the owner write bit:
	// a source that resolved to the Go module cache is read-only, and the
	// staged tree is a scratch copy later steps rewrite (GH-110).
	return os.WriteFile(destination, data, info.Mode().Perm()|0o200)
}

func validateChartProfileReferences(chartRoot string, manifest appmanifest.Manifest) error {
	declared := make(map[string]bool, len(manifest.Deployment.Entries))
	for _, entry := range manifest.Deployment.Entries {
		profile := entry.ProfilePath
		if profile == "" {
			for _, root := range manifest.Services {
				if root.ID == entry.Root {
					profile = root.RuntimePath
					break
				}
			}
		}
		declared[profile] = true
	}
	var selected []string
	templates := filepath.Join(chartRoot, "templates")
	err := filepath.WalkDir(templates, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		for _, match := range chartProfileReferenceRE.FindAllString(string(data), -1) {
			if !declared[match] {
				return fmt.Errorf("chart template %s selects profile %s outside agents/application.yaml",
					filepath.Base(filename), match)
			}
			selected = append(selected, match)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return fmt.Errorf("chart templates select no manifest-declared profiles")
	}
	sort.Strings(selected)
	return nil
}

func validateStagedChatbotComposition(chartRoot string, provenance chatbotPackageProvenance) error {
	if provenance.SchemaVersion != 1 || provenance.Application != chatbotApplicationName ||
		provenance.CompositionManifest != chatbotCompositionManifest ||
		provenance.ManifestChecksum == "" || provenance.MountPath == "" ||
		provenance.Closure.Application != provenance.Application {
		return fmt.Errorf("staged chatbot package provenance has an invalid contract")
	}
	if len(provenance.Files) == 0 || len(provenance.Files) != len(provenance.Closure.Files) {
		return fmt.Errorf("staged chatbot package provenance has an incomplete file inventory")
	}
	for index, file := range provenance.Files {
		if index > 0 && provenance.Files[index-1].RuntimePath >= file.RuntimePath {
			return fmt.Errorf("staged chatbot package files are not deterministic")
		}
		data, err := os.ReadFile(filepath.Join(chartRoot, filepath.FromSlash(file.PackagePath)))
		if err != nil {
			return fmt.Errorf("staged closure missing %s: %w", file.PackagePath, err)
		}
		sum := sha256.Sum256(data)
		if actual := fmt.Sprintf("sha256:%x", sum); actual != file.Checksum {
			return fmt.Errorf("staged closure checksum mismatch for %s: %s != %s",
				file.PackagePath, actual, file.Checksum)
		}
		closureFile := provenance.Closure.Files[index]
		if closureFile.Source != file.Source ||
			closureFile.RuntimePath != file.RuntimePath ||
			closureFile.Checksum != file.Checksum ||
			strings.Join(closureFile.Roots, "\x00") != strings.Join(file.Roots, "\x00") {
			return fmt.Errorf("staged package file %s diverges from shared closure provenance",
				file.PackagePath)
		}
	}
	if len(provenance.TierVariants) != len(chatbotTierVariantSources) {
		return fmt.Errorf("staged chatbot package provenance records %d tier variant(s), want %d",
			len(provenance.TierVariants), len(chatbotTierVariantSources))
	}
	for _, variant := range provenance.TierVariants {
		data, err := os.ReadFile(filepath.Join(chartRoot, filepath.FromSlash(variant.PackagePath)))
		if err != nil {
			return fmt.Errorf("staged tier variant missing %s: %w", variant.PackagePath, err)
		}
		sum := sha256.Sum256(data)
		if actual := fmt.Sprintf("sha256:%x", sum); actual != variant.Checksum {
			return fmt.Errorf("staged tier variant checksum mismatch for %s: %s != %s",
				variant.PackagePath, actual, variant.Checksum)
		}
	}
	return nil
}
