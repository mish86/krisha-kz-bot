package krishakz

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"krisha_kz_bot/pkg/holder"
	"krisha_kz_bot/pkg/parser"

	"github.com/PuerkitoBio/goquery"
)

// TODO move to env
//
//nolint:gochecknoglobals // see time lib implementation
var shortMonthNames = [...]string{
	"янв.", "фев.", "мар.", "апр.", "май", "июн.",
	"июл.", "авг.", "сен.", "окт.", "нояб.", "дек.",
}

type Parser struct {
	TimeZone time.Location
}

func (p *Parser) Parse(payload io.Reader, handler parser.HandlerFunc[holder.WithDT[string]]) error {
	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(payload)
	if err != nil {
		return err
	}

	// almatyLocation := time.FixedZone("UTC+6", +6*60*60)
	// now := time.Now().In(almatyLocation)
	now := time.Now().In(&p.TimeZone)
	log.Printf("Parser, Now in %s %s\n", p.TimeZone.String(), now)

	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, &p.TimeZone)
	today := fmt.Sprintf("%d %s", now.Day(), shortMonthNames[now.Month()-1])

	// Find the review items
	doc.Find("section.a-list.a-search-list div.ddl_product.ddl_product_link").
		Each(func(i int, s *goquery.Selection) {
			isAdOld := false
			statsNodes := s.Find("div.card-stats__item").Nodes

			if len(statsNodes) == 3 || statsNodes[1].FirstChild == nil {
				d := statsNodes[1].FirstChild.Data // 24 окт.
				d = strings.TrimSpace(d)

				// filter by current date
				isAdOld = d != today
			} else {
				log.Println("failed to find advertisement date")
			}

			// skip out of date ads
			if !isAdOld {
				s.Find("a[href].a-card__title").
					Each(func(i int, s *goquery.Selection) {
						// For each item found, get the title
						if href, ok := s.Attr("href"); ok {
							var fnc fncRes = func() (string, time.Time) {
								return href, day
							}

							handler(holder.WithDT[string](fnc))
						}
					})
			}
		})

	return nil
}

type fncRes func() (string, time.Time)

func (f fncRes) GetValue() string {
	href, _ := f()
	return href
}

func (f fncRes) GetDT() time.Time {
	_, dt := f()
	return dt
}
