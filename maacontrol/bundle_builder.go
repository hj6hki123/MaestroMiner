package maacontrol

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kvarenzn/ssm/log"
)

// serverConfig holds the per-game-server configuration loaded from
// resource/servers/{id}.json.
type serverConfig struct {
	// Package is the app package name used for StartApp / StopApp actions.
	Package string `json:"package"`
	// Patches maps "nodeName.fieldName" → replacement JSON value.  Any field
	// of any pipeline node can be overridden; the primary use is OCR expected
	// strings.
	Patches map[string]json.RawMessage `json:"patches"`
}

// buildMergedBundle reads the shared pipeline from resourceDir/pipeline/,
// applies server-specific patches from resourceDir/servers/{gameServer}.json,
// writes the result into a temporary directory and returns its path together
// with a cleanup function that removes it.
//
// The per-server image directory (resourceDir/image/{gameServer}/) is linked
// (symlink) or copied into the temp bundle so MAA can find template images
// via the standard bundle layout: {bundle}/pipeline/*.json + {bundle}/image/...
func buildMergedBundle(resourceDir, gameServer string) (tmpDir string, cleanup func(), err error) {
	// ── 1. load server config ────────────────────────────────────────────────
	cfgPath := filepath.Join(resourceDir, "servers", gameServer+".json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", nil, fmt.Errorf("load server config %q: %w", cfgPath, err)
	}
	var srv serverConfig
	if err := json.Unmarshal(cfgData, &srv); err != nil {
		return "", nil, fmt.Errorf("parse server config %q: %w", cfgPath, err)
	}

	// ── 2. create temp bundle dir ────────────────────────────────────────────
	tmp, err := os.MkdirTemp("", "ssm-bundle-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp bundle: %w", err)
	}
	done := func() { os.RemoveAll(tmp) } //nolint:errcheck

	// ── 3. patch and write pipeline files ───────────────────────────────────
	srcPipeline := filepath.Join(resourceDir, "pipeline")
	dstPipeline := filepath.Join(tmp, "pipeline")
	if err := os.MkdirAll(dstPipeline, 0o755); err != nil {
		done()
		return "", nil, err
	}
	entries, err := os.ReadDir(srcPipeline)
	if err != nil {
		done()
		return "", nil, fmt.Errorf("read pipeline dir %q: %w", srcPipeline, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcPipeline, e.Name()))
		if err != nil {
			done()
			return "", nil, err
		}
		patched, err := patchPipelineFile(raw, &srv)
		if err != nil {
			done()
			return "", nil, fmt.Errorf("patch %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dstPipeline, e.Name()), patched, 0o644); err != nil {
			done()
			return "", nil, err
		}
	}

	// ── 4. link (or copy) per-server image directory ────────────────────────
	srcImage, absErr := filepath.Abs(filepath.Join(resourceDir, "image", gameServer))
	if absErr == nil {
		if _, statErr := os.Stat(srcImage); statErr == nil {
			dstImage := filepath.Join(tmp, "image")
			if linkErr := os.Symlink(srcImage, dstImage); linkErr != nil {
				log.Warnf("[buildMergedBundle] symlink image failed (%v) – copying", linkErr)
				if copyErr := copyDir(srcImage, dstImage); copyErr != nil {
					done()
					return "", nil, fmt.Errorf("copy image dir: %w", copyErr)
				}
			}
		}
	}

	// ── 5. link (or copy) shared model directory ─────────────────────────────
	srcModel, absErr2 := filepath.Abs(filepath.Join(resourceDir, "model"))
	if absErr2 == nil {
		if _, statErr := os.Stat(srcModel); statErr == nil {
			dstModel := filepath.Join(tmp, "model")
			if linkErr := os.Symlink(srcModel, dstModel); linkErr != nil {
				log.Warnf("[buildMergedBundle] symlink model failed (%v) – copying", linkErr)
				if copyErr := copyDir(srcModel, dstModel); copyErr != nil {
					done()
					return "", nil, fmt.Errorf("copy model dir: %w", copyErr)
				}
			}
		}
	}

	log.Infof("[buildMergedBundle] server=%q bundle=%q", gameServer, tmp)
	return tmp, done, nil
}

// patchPipelineFile applies server-specific overrides to a single
// pipeline JSON file and returns the patched JSON.
func patchPipelineFile(data []byte, srv *serverConfig) ([]byte, error) {
	// Parse as a map of node-name → field-map.
	var nodes map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}

	// ── apply dot-notation patches ("nodeName.fieldName") ───────────────────
	for key, val := range srv.Patches {
		dot := strings.IndexByte(key, '.')
		if dot < 0 {
			continue
		}
		nodeName, fieldName := key[:dot], key[dot+1:]
		if node, ok := nodes[nodeName]; ok {
			node[fieldName] = val
		}
	}

	// ── override package in start_app / close_app ───────────────────────────
	if srv.Package != "" {
		pkgJSON, _ := json.Marshal(srv.Package)
		for _, name := range []string{"start_app", "close_app"} {
			if node, ok := nodes[name]; ok {
				node["package"] = json.RawMessage(pkgJSON)
			}
		}
	}

	return json.MarshalIndent(nodes, "", "    ")
}

// copyDir recursively copies src into dst (creating dst if absent).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		return bundleCopyFile(path, dstPath)
	})
}

// bundleCopyFile copies a single file from src to dst.
func bundleCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
