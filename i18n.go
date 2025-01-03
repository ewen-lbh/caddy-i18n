package caddy_i18n

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	mapset "github.com/deckarep/golang-set"
	"github.com/ortfo/gettext/po"
	"go.uber.org/zap"
	"golang.org/x/net/html"
	"golang.org/x/text/language"
)

// translationCatalog holds both the gettext catalog from the .mo file
// and a po file object used to update the .po file (e.g. when discovering new translatable strings)
type translationCatalog struct {
	poFile           *po.File
	seenMessages     mapset.Set
	missingMessages  []po.Message
	language         language.Tag
	sourceLanguage   language.Tag
	poFilesDirectory string
	markerAttribute  string
	markerTag        string
	exposeToJS       bool
	*zap.Logger
}

func (t translationCatalog) poFilePath() string {
	return filepath.Join(t.poFilesDirectory, fmt.Sprintf("%s.po", t.language))
}

func (t translationCatalog) unusedMessagesFilePath() string {
	return filepath.Join(t.poFilesDirectory, fmt.Sprintf("%s-unused-messages.yaml", t.language))
}

type translationsCatalogs map[language.Tag]translationCatalog

func (t *translationCatalog) translatePage(source []byte) (string, error) {
	parsed, err := html.Parse(bytes.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("while parsing output page HTML: %w", err)
	}

	return t.translate(parsed), nil
}

// translate translates the given html node to the target language, removing the translation markers
func (t *translationCatalog) translate(root *html.Node) string {
	// Open files
	doc := goquery.NewDocumentFromNode(root)

	// Expose JS constant
	if t.exposeToJS {
		doc.Find("head").AppendHtml(fmt.Sprintf("<script>window.i18nLanguage = %q; window.i18nSourceLanguage = %q;</script>", t.language, t.sourceLanguage))
	}

	doc.Find(fmt.Sprintf("%s, [%s]", t.markerTag, t.markerAttribute)).Each(func(_ int, element *goquery.Selection) {
		element.RemoveAttr(t.markerAttribute)
		msgContext, _ := element.Attr(fmt.Sprintf("%s-context", t.markerAttribute))
		element.RemoveAttr(fmt.Sprintf("%s-context", t.markerAttribute))
		if t.language != t.sourceLanguage {
			innerHTML, _ := element.Html()
			innerHTML = html.UnescapeString(innerHTML)
			innerHTML = strings.TrimSpace(innerHTML)
			if innerHTML == "" {
				return
			}
			translated, err := t.getTranslation(innerHTML, msgContext)
			if err != nil {
				// color.Yellow("[%s] Missing translation for %q", t.language, innerHTML)

				t.missingMessages = append(t.missingMessages, po.Message{
					MsgId:      innerHTML,
					MsgContext: msgContext,
				})
			} else {
				element.SetHtml(translated)
			}
		}
	})
	doc.Find(fmt.Sprintf("[%s-keep-on]", t.markerAttribute)).Each(func(_ int, element *goquery.Selection) {
		// delete node if the current language is not the value of the attribute
		// useful for conditionally including already-translated content (e.g. user-generated content)
		if element.AttrOr(fmt.Sprintf("%s-keep-on", t.markerAttribute), "") != t.language.String() {
			element.Remove()
		}
		element.RemoveAttr(fmt.Sprintf("%s-keep-on", t.markerAttribute))
	})
	doc.Find(fmt.Sprintf("[%s-attrs]", t.markerAttribute)).Each(func(_ int, element *goquery.Selection) {
		element.RemoveAttr(fmt.Sprintf("%s-attrs", t.markerAttribute))
		// find all attributes that start with "i18n:"
		for _, attribute := range element.Nodes[0].Attr {
			if !strings.HasPrefix(attribute.Key, fmt.Sprintf("%s:", t.markerAttribute)) {
				continue
			}
			if strings.HasPrefix(attribute.Key, fmt.Sprintf("%s:commas:", t.markerAttribute)) {
				// Multi-valued attributes
				translated := attribute.Val
				if t.language != t.sourceLanguage {
					translated = ""
					for _, val := range strings.Split(attribute.Val, ",") {
						translatedItem, err := t.getTranslation(val, "")
						if err != nil {
							t.Warn("missing translation", zap.String("msgid", val))
							t.missingMessages = append(t.missingMessages, po.Message{
								MsgId:      val,
								MsgContext: "",
							})
							translatedItem = val
						}
						translated += "," + translatedItem
					}
					translated = strings.Trim(translated, ",")
				}
				element.RemoveAttr(attribute.Key)
				element.SetAttr(strings.TrimPrefix(attribute.Key, fmt.Sprintf("%s:commas:", t.markerAttribute)), translated)
			} else {
				// Translate the attribute
				translated := attribute.Val
				if t.language != t.sourceLanguage {
					var err error
					translated, err = t.getTranslation(attribute.Val, "")
					if err != nil {
						t.Warn("missing translation", zap.String("msgid", attribute.Val))
						t.missingMessages = append(t.missingMessages, po.Message{
							MsgId:      attribute.Val,
							MsgContext: "",
						})
						translated = attribute.Val
					}
				}
				element.RemoveAttr(attribute.Key)
				element.SetAttr(strings.TrimPrefix(attribute.Key, fmt.Sprintf("%s:", t.markerAttribute)), translated)
			}
		}
	})
	htmlString, _ := doc.Html()
	htmlString = strings.ReplaceAll(htmlString, fmt.Sprintf("<%s>", t.markerTag), "")
	htmlString = strings.ReplaceAll(htmlString, fmt.Sprintf("</%s>", t.markerTag), "")
	return htmlString
}

// loadTranslations reads from i18n/[language].po to load translations
func (m *I18n) loadTranslations() (translationsCatalogs, error) {
	translations := make(translationsCatalogs)
	sourceLanguage, err := language.Parse(m.SourceLanguage)
	if err != nil {
		return translations, fmt.Errorf("invalid source language code: %w", err)
	}

	for _, languageCodeStr := range m.Languages {
		languageCode, err := language.Parse(languageCodeStr)
		if err != nil {
			return translations, fmt.Errorf("invalid language code %q: %w", languageCodeStr, err)
		}

		translationsFilepath := fmt.Sprintf("%s/%s.po", m.Translations, languageCode)
		poFile, err := po.LoadFile(translationsFilepath)
		if err != nil {
			return nil, fmt.Errorf("while loading translations for %s: %w", languageCode, err)
		}

		poFile.SetSourceLanguage(sourceLanguage)
		translations[languageCode] = translationCatalog{
			poFile:           poFile,
			seenMessages:     mapset.NewSet(),
			missingMessages:  make([]po.Message, 0),
			language:         languageCode,
			sourceLanguage:   sourceLanguage,
			poFilesDirectory: m.Translations,
			markerAttribute:  m.HTMLAttribute,
			markerTag:        m.HTMLTag,
			exposeToJS:       m.ExposeToJS,
			Logger:           m.Logger.With(zap.String("lang", languageCode.String())),
		}
		filledTranslationsCount := 0
		for _, msg := range poFile.Messages {
			if msg.MsgId != "" {
				filledTranslationsCount++
			}
		}
		m.Logger.With(zap.String("lang", languageCode.String())).Info(fmt.Sprintf("loaded %d translations", filledTranslationsCount))
	}
	return translations, nil
}

func (t translationCatalog) unusedMessages() []po.Message {
	unused := make([]po.Message, 0)
	for _, message := range t.poFile.Messages {
		if !t.seenMessages.Contains(message.MsgId + message.MsgContext) {
			unused = append(unused, message)
		}
	}
	return unused
}

func (t translationCatalog) writeUnusedMessages() (count int, err error) {
	unused := t.unusedMessages()
	count = len(unused)

	if count == 0 {
		os.Remove(t.unusedMessagesFilePath())
	} else {
		file, err := os.OpenFile(t.unusedMessagesFilePath(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		os.WriteFile(t.unusedMessagesFilePath(), []byte("# Generated at "+time.Now().String()+"\n"), 0644)
		for _, message := range unused {
			if message.MsgContext != "" {
				_, err = file.WriteString(fmt.Sprintf("- {msgid: %q, msgctxt: %q}\n", message.MsgId, message.MsgContext))
			} else {
				_, err = file.WriteString(fmt.Sprintf("- %q\n", message.MsgId))
			}
			if err != nil {
				return 0, err
			}
		}
	}
	return count, nil
}

// func (t *Translations) deleteUnusedMessages() {
// 	for _, message := range t.unusedMessages() {
// 		for i, msg := range t.poFile.Messages {
// 			if msg.MsgId == message.MsgId && msg.MsgContext == message.MsgContext {
// 				t.poFile.Messages[i] = t.poFile.Messages[len(t.poFile.Messages)-1]
// 				t.poFile.Messages = t.poFile.Messages[:len(t.poFile.Messages)-1]
// 			}
// 		}
// 	}
// }

// savePO writes the .po file to the disk, with its potential modifications
// It removes duplicate messages beforehand
func (t *translationCatalog) savePO() {
	// TODO: sort file after saving, (po.File).Save is not stable... (creates unecessary diffs in git)
	// Remove unused messages with empty msgstrs
	uselessRemoved := make([]po.Message, 0)
	for _, msg := range t.poFile.Messages {
		if !t.seenMessages.Contains(msg.MsgId+msg.MsgContext) && msg.MsgStr == "" {
			t.seenMessages.Remove(msg.MsgId + msg.MsgContext)
			continue
		}
		uselessRemoved = append(uselessRemoved, msg)
	}
	t.poFile.Messages = uselessRemoved
	// Add missing messages
	t.poFile.Messages = append(t.poFile.Messages, t.missingMessages...)
	// Remove duplicate messages
	dedupedMessages := make([]po.Message, 0)
	for _, msg := range t.poFile.Messages {
		var isDupe bool
		for _, msg2 := range dedupedMessages {
			if msg.MsgId == msg2.MsgId && msg.MsgContext == msg2.MsgContext {
				isDupe = true
			}
		}
		if !isDupe {
			dedupedMessages = append(dedupedMessages, msg)
		}
	}
	// Sort them to guarantee a stable write
	t.poFile.Messages = dedupedMessages
	t.poFile.Save(t.poFilePath())
}

// getTranslation returns the msgstr corresponding to msgid and msgctxt from the .po file
// If not found, it returns an error
func (t translationCatalog) getTranslation(msgid string, msgctxt string) (string, error) {
	if msgid == "" {
		return "", nil
	}
	t.seenMessages.Add(msgid + msgctxt)
	for _, message := range t.poFile.Messages {
		if message.MsgId == msgid && message.MsgStr != "" && message.MsgContext == msgctxt {
			return message.MsgStr, nil
		}
	}
	return "", fmt.Errorf("cannot find msgstr in %s with msgid=%q and msgctx=%q", t.language, msgid, msgctxt)
}
