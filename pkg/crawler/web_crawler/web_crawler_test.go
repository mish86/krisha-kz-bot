package webcrawler_test

import (
	"context"
	"fmt"
	"io"
	webcrawler "krisha_kz_bot/pkg/crawler/web_crawler"
	"krisha_kz_bot/pkg/parser"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type testUnit struct {
	respHandler http.HandlerFunc
	parser      parser.Func[string]
}

func newTestUnit(t *testing.T, payload string) *testUnit {
	return &testUnit{
		respHandler: func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write([]byte(payload)); err != nil {
				t.Errorf("failed to write response, got error %v", err)
			}
		},
		parser: parser.Func[string](func(payload io.Reader, handler parser.HandlerFunc[string]) error {
			buf := new(strings.Builder)
			if _, err := io.Copy(buf, payload); err != nil {
				return err
			}
			for _, val := range strings.Split(buf.String(), "\n") {
				handler(val)
			}
			return nil
		}),
	}
}

type TestCase struct {
	testUnit
	Payload  string        `yaml:"Payload"`
	Want1    []string      `yaml:"Want1"`
	Interval time.Duration `yaml:"Interval"`
	Timeout  time.Duration `yaml:"Timeout"`
	Want2    []string      `yaml:"Want2"`
}

func TestWebCrawler(t *testing.T) {
	const testFile = "./test_data/test_data.yaml"

	var cases []TestCase

	if data, err1 := os.ReadFile(testFile); err1 != nil {
		t.Fatalf("failed to read file %s, got error %v", testFile, err1)
	} else if err2 := yaml.Unmarshal(data, &cases); err2 != nil {
		t.Fatalf("failed to unmarshal test cases from %s, got error %v", testFile, err2)
	}

	for i, c := range cases {
		t.Run(fmt.Sprintf("DoCrawl, test case %d", i), func(t *testing.T) {
			testDoCrawl(t, i, &c)
		})

		t.Run(fmt.Sprintf("Start and Stop, test case %d", i), func(t *testing.T) {
			testStartStop(t, i, &c)
		})
	}
}

func mockSrcAndCrawler(c *TestCase) (*httptest.Server, *webcrawler.WebCrawler[string]) {
	srv := httptest.NewServer(c.respHandler)

	webCraler := webcrawler.NewCrawler(
		&webcrawler.Config[string]{
			Interval: c.Interval,
			Parser:   c.parser,
		},
		[]string{srv.URL},
		srv.Client(),
	)

	return srv, webCraler
}

func checkResult(t *testing.T, caseNum int, want []string, resCh <-chan string) {
	er := make([]string, len(want))
	copy(er, want)

	got := make([]string, 0, len(want))

	for val := range resCh {
		got = append(got, val)

		for i, want := range er {
			if want == val {
				er = append(er[:i], er[i+1:]...)
				break
			}
		}
	}

	if len(er) != 0 || len(want) != len(got) {
		t.Errorf("not all results in case %d, want %v, got %v", caseNum, want, got)
	}
}

func testDoCrawl(t *testing.T, caseNum int, c *TestCase) {
	c.testUnit = *newTestUnit(t, c.Payload)

	mockSrv, webCrawler := mockSrcAndCrawler(c)
	defer mockSrv.Close()

	resCh := make(chan string)
	go func(crawler *webcrawler.WebCrawler[string], url string, resCh chan string) {
		defer close(resCh)

		if err := crawler.DoCrawl(context.Background(), url, resCh); err != nil {
			t.Errorf("failed to crawl, got error %v", err)
		}
	}(webCrawler, mockSrv.URL, resCh)

	checkResult(t, caseNum, c.Want1, resCh)
}

func testStartStop(t *testing.T, caseNum int, c *TestCase) {
	c.testUnit = *newTestUnit(t, c.Payload)

	mockSrv, webCrawler := mockSrcAndCrawler(c)
	defer mockSrv.Close()

	resCh := webCrawler.Start(context.Background())

	go func() {
		defer webCrawler.Stop()

		stopTimer := time.NewTimer(c.Timeout)
		defer stopTimer.Stop()

		<-stopTimer.C
	}()

	checkResult(t, caseNum, c.Want2, resCh)
}
