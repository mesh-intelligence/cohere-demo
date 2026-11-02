// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The profiles a mesh runs are the largest thing a release carries, and almost
// none of it is values-driven: the declarations, machines, and rest definitions
// are the same bytes for every install of one package. Left in the release they
// are stored twice over -- once as chart files and again as the rendered
// profile ConfigMaps -- and a 1 MiB Secret limit that is mostly declarations
// blocks every feature that adds one (GH-785, the wall GH-737 hit at 20 bytes).
//
// So the verbatim profiles travel the way the UI bundles already do (GH-1475):
// out of release, in ConfigMaps the deployment tooling provisions and names in
// values, mounted through the same projected volume as before. What stays in
// the release is what the release decides: the co-generated keys, which are
// rendered from values at install time (srd003 R2), and the files the chart
// itself reads with Files.Get to render them.
//
// The mount path, the file layout, and the profile closure a pod loads are
// unchanged. A pod sees one profiles tree whichever half a file came from.

// servedUIProfilePrefix is the one staged subtree that is not a profile: the
// chatbot's built bundle, which the agent serves from its own UI ConfigMap
// (GH-131) and which externalizeUIAssets carries as an archive.
const servedUIProfilePrefix = "agents__chatbot__ui__app__dist__"

// externalProfileGroup is one ConfigMap group's verbatim profiles, moved out of
// the release. Keys are the ConfigMap keys the chart projects, each mapping to
// its path under the profiles root by the same "__" -> "/" rule the
// co-generated keys use.
type externalProfileGroup struct {
	Group         string
	ConfigMapName string
	Dir           string
	Keys          []string
	Checksum      string
}

// profilesExternalizationResult carries the groups and the values file that
// names them, so a caller adds one flag to its helm arguments.
type profilesExternalizationResult struct {
	Groups     []externalProfileGroup
	ValuesFile string
}

// externalizeProfiles moves every verbatim staged profile out of the chart for
// releaseName and returns the groups to provision. It runs after
// externalizeUIAssets, whose files leave the tree first.
//
// What a file does decides where it goes. A file the chart reads with Files.Get
// is an input to a render and stays, whether or not its own key also travels. A
// file whose key the ConfigMap serves rewritten stays and does not travel at
// all, because the bytes a pod reads are not the bytes on disk. A file whose key
// is co-generated is delivered by that render, so unless the render also reads
// it the packaged copy is dead weight and is deleted. Everything else moves.
func externalizeProfiles(chart, releaseName string) (profilesExternalizationResult, func(), error) {
	var result profilesExternalizationResult
	if strings.TrimSpace(releaseName) == "" {
		return result, nil, fmt.Errorf("external profiles release name is empty")
	}
	cogenerated, err := coGeneratedProfileKeys(chart)
	if err != nil {
		return result, nil, err
	}
	rewritten, err := chartProfileKeyList(chart, "chatbot-mesh.renderedProfileKeys")
	if err != nil {
		return result, nil, err
	}
	rendered, err := chartRenderedProfileSources(chart)
	if err != nil {
		return result, nil, err
	}
	dir, err := os.MkdirTemp("", "chatbot-mesh-external-profiles-*")
	if err != nil {
		return result, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	profilesRoot := filepath.Join(chart, "profiles")
	groups := map[string]*externalProfileGroup{}
	var dead []string
	walkErr := filepath.Walk(profilesRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, profilesRoot+string(filepath.Separator)))
		source := "profiles/" + relative
		key := strings.ReplaceAll(relative, "/", "__")
		if strings.HasPrefix(key, servedUIProfilePrefix) {
			// The served bundle is projected from the agent's own UI ConfigMap
			// (GH-131) and externalized as an archive beside it; it is not part
			// of any pod's profile closure.
			return nil
		}
		if rewritten[key] {
			// The ConfigMap serves this key from the packaged file with the
			// installed release's coordinates substituted in. The bytes a pod
			// reads are not the bytes on disk, so the file stays where the
			// render finds it and the key does not travel.
			return nil
		}
		if cogenerated[key] {
			if rendered[source] {
				// A co-generated key whose own file the render reads: the file
				// is an input, the key is the render's output.
				return nil
			}
			// The render supplies this key; the packaged copy is never read.
			dead = append(dead, path)
			return nil
		}
		group := profileGroupOf(relative)
		entry, ok := groups[group]
		if !ok {
			entry = &externalProfileGroup{Group: group, Dir: filepath.Join(dir, group)}
			if mkErr := os.MkdirAll(entry.Dir, 0o755); mkErr != nil {
				return mkErr
			}
			groups[group] = entry
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if writeErr := os.WriteFile(filepath.Join(entry.Dir, key), data, 0o644); writeErr != nil {
			return writeErr
		}
		entry.Keys = append(entry.Keys, key)
		if rendered[source] {
			// The tier variants are both projected under their own keys and
			// read by the render that picks one, so this file travels as a
			// ConfigMap entry and stays in the chart as a render input. The
			// duplicate costs nothing the release was not already carrying.
			return nil
		}
		return os.Remove(path)
	})
	if walkErr != nil {
		cleanup()
		return result, nil, fmt.Errorf("externalize profiles: %w", walkErr)
	}
	for _, path := range dead {
		if err := os.Remove(path); err != nil {
			cleanup()
			return result, nil, fmt.Errorf("remove co-generated profile copy: %w", err)
		}
	}
	if len(groups) == 0 {
		cleanup()
		return result, nil, fmt.Errorf("no verbatim profiles found under %s", profilesRoot)
	}

	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	for _, group := range names {
		entry := groups[group]
		sort.Strings(entry.Keys)
		checksum, err := profileGroupChecksum(entry)
		if err != nil {
			cleanup()
			return result, nil, err
		}
		entry.Checksum = checksum
		entry.ConfigMapName = fmt.Sprintf("%s-profiles-%s-%s", releaseName, group, checksum[:12])
		result.Groups = append(result.Groups, *entry)
	}
	valuesFile := filepath.Join(dir, "external-profiles.yaml")
	if err := writeExternalProfileValues(valuesFile, result.Groups); err != nil {
		cleanup()
		return result, nil, err
	}
	result.ValuesFile = valuesFile
	return result, cleanup, nil
}

// profileGroupOf names the ConfigMap group a staged profile belongs to, the
// same split the chart's own glob makes: one group per agent root, and one for
// the applications subtree the wrapper profiles live in.
func profileGroupOf(relative string) string {
	if strings.HasPrefix(relative, "applications/") {
		return "applications"
	}
	parts := strings.Split(relative, "/")
	if len(parts) > 1 && parts[0] == "agents" {
		return parts[1]
	}
	return "applications"
}

// profileGroupChecksum is the identity of a group's content: every key and the
// hash of its bytes, in key order. It names the ConfigMap, so a changed profile
// produces a new object rather than mutating one a running pod has mounted.
func profileGroupChecksum(group *externalProfileGroup) (string, error) {
	digest := sha256.New()
	for _, key := range group.Keys {
		data, err := os.ReadFile(filepath.Join(group.Dir, key))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(digest, "%s:%x\n", key, sha256.Sum256(data))
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

// writeExternalProfileValues records each group's ConfigMap and the keys the
// chart projects from it. The keys ride in values rather than being derived in
// the chart because the files are no longer there to enumerate; they cost a few
// kilobytes and save several hundred.
func writeExternalProfileValues(path string, groups []externalProfileGroup) error {
	external := map[string]any{}
	for _, group := range groups {
		external[group.Group] = map[string]any{
			"configMap": group.ConfigMapName,
			"checksum":  group.Checksum,
			"keys":      group.Keys,
		}
	}
	document := map[string]any{
		"profiles": map[string]any{"external": external},
	}
	data, err := yaml.Marshal(document)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// externalProfileValueArgs points the chart at the provisioned groups.
func externalProfileValueArgs(result profilesExternalizationResult) []string {
	if result.ValuesFile == "" {
		return nil
	}
	return []string{"--values", result.ValuesFile}
}

// provisionExternalProfiles creates each group's ConfigMap outside the release.
// The name carries the content checksum, so an install that changed a profile
// writes a new object and the stale one is removed by name only when it is the
// same object this release would have created.
func provisionExternalProfiles(run helmLLMCommandRunner, result profilesExternalizationResult) error {
	for _, group := range result.Groups {
		if out, err := run(
			"kubectl", "delete", "configmap", group.ConfigMapName, "--ignore-not-found"); err != nil {
			return fmt.Errorf("clear stale %s profiles ConfigMap %s: %w: %s",
				group.Group, group.ConfigMapName, err, strings.TrimSpace(string(out)))
		}
		if out, err := run("kubectl", "create", "configmap", group.ConfigMapName,
			"--from-file="+group.Dir); err != nil {
			return fmt.Errorf("create %s profiles ConfigMap %s: %w: %s",
				group.Group, group.ConfigMapName, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// coGeneratedProfileKeys reads the chart's own list, so the Go side and the
// templates cannot disagree about which keys a render supplies. The helper is
// one line of the chart; parsing it is cheaper than a second copy that drifts.
func coGeneratedProfileKeys(chart string) (map[string]bool, error) {
	return chartProfileKeyList(chart, "chatbot-mesh.cogeneratedProfileKeys")
}

// chartProfileKeyList reads one of the chart's own space-separated key lists.
// Parsing the template is how the Go side and the chart are kept from
// disagreeing about which keys the release decides; a second copy here would
// drift, and the drift would show up as a profile missing from a running pod.
func chartProfileKeyList(chart, define string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(chart, "templates", "_helpers.tpl"))
	if err != nil {
		return nil, fmt.Errorf("read chart helpers: %w", err)
	}
	marker := fmt.Sprintf("{{- define %q -}}", define)
	body := string(data)
	start := strings.Index(body, marker)
	if start < 0 {
		return nil, fmt.Errorf("the chart declares no %s", define)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, "{{- end -}}")
	if end < 0 {
		return nil, fmt.Errorf("the %s list is unterminated", define)
	}
	keys := map[string]bool{}
	for _, key := range strings.Fields(rest[:end]) {
		keys[key] = true
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("the %s list is empty", define)
	}
	return keys, nil
}

// chartRenderedProfileSources names the staged files the templates read with
// Files.Get. Those are inputs to a render -- the tier-selected machines and the
// declarations the ConfigMap rewrites -- so they stay in the chart whatever
// their ConfigMap key.
func chartRenderedProfileSources(chart string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(chart, "templates"))
	if err != nil {
		return nil, fmt.Errorf("read chart templates: %w", err)
	}
	sources := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(chart, "templates", entry.Name()))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			index := strings.Index(line, `Files.Get "profiles/`)
			for index >= 0 {
				rest := line[index+len(`Files.Get "`):]
				quote := strings.Index(rest, `"`)
				if quote < 0 {
					break
				}
				sources[rest[:quote]] = true
				line = rest[quote+1:]
				index = strings.Index(line, `Files.Get "profiles/`)
			}
		}
	}
	return sources, nil
}
