// Package i18n serves UI and API message bundles. Adding a language is a
// matter of dropping a new locales/<code>.json file next to the others.
package i18n

import (
	"embed"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var files embed.FS

var (
	once    sync.Once
	bundles map[string]map[string]string
)

func load() {
	bundles = map[string]map[string]string{}
	entries, err := files.ReadDir("locales")
	if err != nil {
		return
	}
	for _, e := range entries {
		code := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		b, err := files.ReadFile("locales/" + e.Name())
		if err != nil {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		bundles[code] = m
	}
}

func Languages() []string {
	once.Do(load)
	out := make([]string, 0, len(bundles))
	for k := range bundles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Bundle(lang string) (map[string]string, bool) {
	once.Do(load)
	b, ok := bundles[lang]
	return b, ok
}

// T translates key for lang, falling back to Azerbaijani and then the key itself.
func T(lang, key string) string {
	once.Do(load)
	if b, ok := bundles[lang]; ok {
		if v, ok := b[key]; ok {
			return v
		}
	}
	if b, ok := bundles["az"]; ok {
		if v, ok := b[key]; ok {
			return v
		}
	}
	return key
}

// Pick resolves the language from explicit value, then Accept-Language, then default.
func Pick(explicit, acceptLanguage, def string) string {
	once.Do(load)
	if _, ok := bundles[explicit]; ok {
		return explicit
	}
	for _, part := range strings.Split(acceptLanguage, ",") {
		code := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if i := strings.Index(code, "-"); i > 0 {
			code = code[:i]
		}
		if _, ok := bundles[code]; ok {
			return code
		}
	}
	return def
}
