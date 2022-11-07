package main

import (
	"context"
	"errors"
	"fmt"
	"krisha_kz_bot/pkg/bot"
	"krisha_kz_bot/pkg/cleaner"
	webcrawler "krisha_kz_bot/pkg/crawler/web_crawler"
	"krisha_kz_bot/pkg/holder"
	"krisha_kz_bot/pkg/id"
	krishakz "krisha_kz_bot/pkg/parser/krisha_kz"
	"krisha_kz_bot/pkg/scanner"
	"krisha_kz_bot/pkg/serv"
	"krisha_kz_bot/pkg/utils"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v9"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Application struct {
	scanServ *scanner.Service[string]
	botServ  *bot.Service

	shutdowners []serv.Shutdowner

	cleansingServ cleaner.Cleansinger
	cleaners      []cleaner.Cleaner

	rdb *redis.Client
}

func main() {
	app := &Application{}

	setupScanServ(app)
	setupBotServ(app)

	setupRedis(app)

	setupShutdowners(app)
	setupCleansing(app)

	startWithGS(app)
}

func setupScanServ(app *Application) {
	var scanInterval time.Duration = utils.ParseEnvOrPanic[time.Duration]("SCANNER_INTERVAL")
	var scanTimeZone time.Location = utils.ParseEnvOrPanic[time.Location]("SCANNER_TIME_ZONE")
	var visitedBufSize int = utils.ParseEnvOrPanic[int]("SCANNER_VISITED_BUF_SIZE")
	var retentionPolicy time.Duration = utils.ParseEnvOrPanic[time.Duration]("SCANNER_RETENTION_POLICY")

	app.scanServ = scanner.NewServiceFromConfig(
		&scanner.Config[string]{
			TimeZone:        scanTimeZone,
			VisitedBufSize:  visitedBufSize,
			RetentionPolicy: retentionPolicy,
			Config: webcrawler.Config[holder.WithDT[string]]{
				Interval: scanInterval,
				// Parser:   parser.Func[string](krishakz.Parse),
				Parser: &krishakz.Parser{
					TimeZone: scanTimeZone,
				},
			},
			OnResult: func(key id.Key, href string) {
				text := fmt.Sprintf("@%s pls look at https://krisha.kz%s\n", key.UserName, href)

				if err := app.botServ.SendMessage(tgbotapi.NewMessage(int64(key.ChatID), text)); err != nil {
					log.Printf("failed to send message, error %v\n", err)
				}
			},
		},
	)
}

//nolint:gocognit // TODO
func setupBotServ(app *Application) {
	var (
		err error

		botAPIToken     string        = utils.ParseEnvOrPanic[string]("BOT_API_TOKEN")
		botSendMsgBuf   int           = utils.ParseEnvOrPanic[int]("BOT_SEND_MSG_BUFFER")
		botSendMsgDelay time.Duration = utils.ParseEnvOrPanic[time.Duration]("BOT_SEND_MSG_DELAY")

		scanPagesCnt int = utils.ParseEnvOrPanic[int]("SCANNER_PAGES")
	)

	if app.botServ, err = bot.NewServiceFromConfig(&bot.Config{
		Token:        botAPIToken,
		UpdateConfig: tgbotapi.UpdateConfig{Timeout: bot.DefaultUpdateTimeout},
		Debug:        true,
		SendMsgBuf:   botSendMsgBuf,
		SendMsgDelay: botSendMsgDelay,
		HandlersConfig: bot.HandlersConfig{
			OnStop: func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
				if e1 := app.scanServ.UnRegister(key); e1 != nil {
					switch {
					case errors.Is(e1, scanner.ErrNotExist):
						text := fmt.Sprintf("@%s not subscribed", key.UserName)
						return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
					default:
						text := fmt.Sprintf("failed to unsubscribe @%s", key.UserName)
						return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
					}
				}

				return tgbotapi.NewMessage(int64(key.ChatID), fmt.Sprintf("Subscription stopped for @%s", key.UserName)), nil
			},
			OnKicked: func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
				if e1 := app.scanServ.UnRegister(key); e1 != nil {
					e2 := fmt.Errorf("failed to unsubscribe @%s, error %w", key.UserName, e1)
					log.Println(e2)
					return nil, err
				}

				return nil, nil
			},
			OnSubscribe: func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
				var (
					textErrParse                    = fmt.Sprintf("@%s failed to parse url", key.UserName)
					respErrParse tgbotapi.Chattable = tgbotapi.NewMessage(int64(key.ChatID), textErrParse)
					errParse                        = errors.New(textErrParse)
				)

				if len(params) < 1 {
					return respErrParse, errParse
				}

				var filter url.URL
				if res, ok := params[0].(url.URL); ok {
					filter = res
				} else {
					return respErrParse, errParse
				}

				urls := make([]string, scanPagesCnt)

				for i := 0; i < scanPagesCnt; i++ {
					u := filter
					vals := u.Query()
					vals.Add("page", strconv.Itoa(i+1))
					u.RawQuery = vals.Encode()

					urls[i] = u.String()
				}

				if e1 := app.scanServ.Register(key, urls); e1 != nil {
					switch {
					case errors.Is(e1, scanner.ErrExist):
						text := fmt.Sprintf("@%s already subscribed", key.UserName)
						return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
					default:
						text := fmt.Sprintf("failed to subscribe @%s", key.UserName)
						return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
					}
				} else {
					if e2 := app.scanServ.Start(context.Background(), key); e2 != nil {
						switch {
						case errors.Is(e2, scanner.ErrNotExist):
							text := fmt.Sprintf("@%s not subscribed, failed to start scanning", key.UserName)
							return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
						default:
							_ = app.scanServ.UnRegister(key)
							text := fmt.Sprintf("failed to start scanning for @%s", key.UserName)
							return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
						}
					}

					return tgbotapi.NewMessage(int64(key.ChatID), fmt.Sprintf("@%s subscribed for notifications", key.UserName)), nil
				}
			},
		},
	}); err != nil {
		log.Panicf("failed to setup bot service, error %v\n", err)
	}
}

func setupCleansing(app *Application) {
	app.cleaners = []cleaner.Cleaner{app.botServ, app.scanServ}
	app.cleansingServ = cleaner.CleansingFnc(cleansing)
}

func cleansing(ctx context.Context, interval time.Duration, onTimer func()) {
	retryTimer := time.NewTimer(interval)
	defer retryTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("stopping cleansing\n")
			return

		case <-retryTimer.C:
			onTimer()

			// reset timer
			retryTimer.Reset(interval)
		}
	}
}

func setupShutdowners(app *Application) {
	app.shutdowners = []serv.Shutdowner{
		app.scanServ,
		app.botServ,
		serv.ShutdownerFunc(func() error {
			log.Println("shutting down redis client")
			return app.rdb.Close()
		}),
	}
}

func setupRedis(app *Application) {
	var (
		redisURL string = utils.ParseEnvOrPanic[string]("REDIS_URL")
	)

	if opt, err := redis.ParseURL(redisURL); err == nil {
		app.rdb = redis.NewClient(opt)
	} else {
		log.Panicf("failed to parse redis url %s, error %v", redisURL, err)
	}

	app.scanServ.WithRedis(app.rdb)
	app.botServ.WithRedis(app.rdb)
}

func startWithGS(app *Application) {
	c := make(chan os.Signal, 1)
	// graceful shutdown
	// when SIGINT (Ctrl+C)
	// when SIGTERM (Ctrl+/)
	// except SIGKILL, SIGQUIT will not be caught
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

	var cleansingInterval time.Duration = utils.ParseEnvOrPanic[time.Duration]("CACHE_CLEANSING_INTERVAL")

	// run non-blocking cleansing
	go app.cleansingServ.Start(context.Background(), cleansingInterval, func() {
		for _, cleaner := range app.cleaners {
			cleaner.Clean()
		}
	})

	// run non-blocking serv
	go func() {
		// start telegram bot
		if err := app.botServ.Start(context.Background()); err != nil {
			log.Panic(err)
		} else {
			log.Println("bot service stoppped")
		}
	}()

	// load from persistance storage
	app.botServ.LoadFromRedis(context.Background())

	// block until we receive our signal.
	<-c

	const (
		defaultGSTimeout = time.Second * 15
	)

	var (
		gsTimeout time.Duration
		err       error
	)

	if gsTimeout, err = time.ParseDuration(os.Getenv("GRACEFUL_SHUTDOWN_TIMEOUT")); err != nil {
		gsTimeout = defaultGSTimeout
	}

	log.Println("starting shut down")
	ctx, cancel := context.WithTimeout(context.Background(), gsTimeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(len(app.shutdowners))

	for _, shutdowner := range app.shutdowners {
		go func(shutdowner serv.Shutdowner) {
			defer wg.Done()

			if e := shutdowner.Shutdown(); e != nil {
				log.Println(e.Error())
			}
		}(shutdowner)
	}

	go func() {
		wg.Wait()
		cancel()
	}()

	// block until all services completed their work or timeout
	<-ctx.Done()
	if e := ctx.Err(); e != nil {
		log.Println(e.Error())
	}

	log.Println("ending shut down")
	// os.Exit(0)
}
