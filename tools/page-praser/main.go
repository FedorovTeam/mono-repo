// tools/page-praser/main.go
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	PageURL = "https://iltumen.ru/sessions/plenary/85"
	BaseURL = "https://iltumen.ru"
	SaveDir = "documents"
)

func main() {
	if err := os.MkdirAll(SaveDir, 0755); err != nil {
		panic(err)
	}

	links, err := extractLinksWithChrome(PageURL)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Найдено документов: %d\n", len(links))

	client := &http.Client{Timeout: 60 * time.Second}

	for _, l := range links {
		if err := downloadFile(client, l, PageURL); err != nil {
			fmt.Printf("Ошибка скачивания %s: %v\n", l, err)
		}
	}
}

func extractLinksWithChrome(pageURL string) ([]string, error) {
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var html string
	err := chromedp.Run(ctx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(3*time.Second),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(`href="([^"]*/api/containers/plenarysessions/download/[^"]+)"`)
	matches := re.FindAllStringSubmatch(html, -1)

	seen := map[string]struct{}{}
	var out []string
	for _, m := range matches {
		href := m[1]
		full := resolveURL(BaseURL, href)
		if _, ok := seen[full]; ok {
			continue
		}
		seen[full] = struct{}{}
		out = append(out, full)
	}
	return out, nil
}

func resolveURL(base, ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		return u.String()
	}
	b, _ := url.Parse(base)
	return b.ResolveReference(u).String()
}

func downloadFile(client *http.Client, fileURL string, referer string) error {
	req, _ := http.NewRequest("GET", fileURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	filename := filenameFromHeaders(resp, fileURL)
	filename = ensureExtensionByContentType(filename, resp.Header.Get("Content-Type"))

	filePath := uniquePath(SaveDir, filename)
	fmt.Println("Скачиваю:", filepath.Base(filePath))

	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
