package caddy_i18n

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"golang.org/x/text/language"
)

var _ caddy.Provisioner = (*I18n)(nil)
var _ caddy.Validator = (*I18n)(nil)
var _ caddyhttp.MiddlewareHandler = (*I18n)(nil)

type I18n struct {
	Translations       string   `json:"translations,omitempty"`
	HTMLAttribute      string   `json:"html_attribute,omitempty"`
	HTMLTag            string   `json:"html_tag,omitempty"`
	SourceLanguage     string   `json:"source_language,omitempty"`
	Languages          []string `json:"languages,omitempty"`
	UpdateTranslations bool     `json:"update_translations,omitempty"`

	catalog         *translationsCatalogs
	tagToCatalogKey map[language.Tag]string
	languageMatcher language.Matcher
	*zap.Logger
}

var defaultConfig = I18n{
	Translations:       "i18n",
	HTMLAttribute:      "i18n",
	HTMLTag:            "i18n",
	SourceLanguage:     "en",
	tagToCatalogKey:    make(map[language.Tag]string),
	UpdateTranslations: false,
}

func init() {
	caddy.RegisterModule(I18n{})
	httpcaddyfile.RegisterHandlerDirective("i18n", parseCaddyfileHandler)
}

func (I18n) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "http.handlers.i18n",
		New: func() caddy.Module {
			module := defaultConfig
			return &module
		},
	}
}

func (m *I18n) Provision(ctx caddy.Context) error {
	// Load all .po files in the
	m.Logger = ctx.Logger()
	catalog, err := m.loadTranslations()
	if err != nil {
		return fmt.Errorf("while loading translations: %w", err)
	}

	if len(catalog) < len(m.Languages) {
		return fmt.Errorf("not all declared languages have translations")
	}

	availableLanguages := make([]language.Tag, 0, len(catalog))
	for lang := range catalog {
		parsed, err := language.Parse(lang)
		if err != nil {
			return fmt.Errorf("incorrect language: %w", err)
		}
		m.tagToCatalogKey[parsed] = lang
		availableLanguages = append(availableLanguages, parsed)
	}
	m.languageMatcher = language.NewMatcher(availableLanguages)
	m.catalog = &catalog
	return nil
}

func (m *I18n) Validate() error {
	if len(m.Languages) == 0 {
		return fmt.Errorf("no languages provided. Use languages directive to specify languages you support (languages that have an LANGUAGE.po file in the translations directory, which can be be configured with the translations directive) list (spaces separated)")
	}

	if stat, err := os.Stat(m.Translations); err != nil || !stat.IsDir() {
		return fmt.Errorf("translations directory does not exist or is not a directory")
	}

	return nil
}

func shouldBuffer(status int, header http.Header) bool {
	return status >= 200 && status < 400 && strings.HasPrefix(header.Get("Content-Type"), "text/html")
}

func (m *I18n) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	untranslated := new(bytes.Buffer)
	recorder := caddyhttp.NewResponseRecorder(w, untranslated, shouldBuffer)
	err := next.ServeHTTP(recorder, r)
	if err != nil {
		return err
	}
	if !recorder.Buffered() {
		return nil
	}

	acceptedLanguages := r.Header.Get("Accept-Language")
	lang, _ := language.MatchStrings(m.languageMatcher, acceptedLanguages)

	translations := (*m.catalog)[m.tagToCatalogKey[lang]]
	w.Header().Set("Language", translations.language)

	translated, err := translations.translatePage(untranslated.Bytes())
	if err != nil {
		return fmt.Errorf("could not translate %s to %s: %w", r.RequestURI, lang, err)
	}

	w.WriteHeader(recorder.Status())
	_, err = io.WriteString(w, translated)
	if err != nil {
		return fmt.Errorf("while writing translated response to %s in %s: %w", r.RequestURI, lang, err)
	}

	if m.UpdateTranslations {
		translations.savePO()
	}

	translations.writeUnusedMessages()

	return nil
}

func parseCaddyfileHandler(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	config := defaultConfig
	module := &config
	d := h.Dispenser

	// Consume directive name
	d.Next()

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "translations":
			if !d.NextArg() {
				return module, fmt.Errorf("missing argument for translations directory")
			}

			module.Translations = d.Val()

		case "html_attribute":
			if !d.NextArg() {
				return module, fmt.Errorf("languages is missing a value")
			}
			module.HTMLAttribute = d.Val()

		case "html_tag":
			if !d.NextArg() {
				return module, fmt.Errorf("source_language is missing a value")
			}
			module.HTMLTag = d.Val()

		case "source_language":
			if !d.NextArg() {
				return module, fmt.Errorf("html_tag is missing a value")
			}
			module.SourceLanguage = d.Val()

		case "languages":
			if !d.NextArg() {
				return module, fmt.Errorf("html_attribute is missing a value")
			}
			module.Languages = strings.Split(d.Val(), ",")

		case "update_translations":
			module.UpdateTranslations = true

		}
	}

	return module, nil
}