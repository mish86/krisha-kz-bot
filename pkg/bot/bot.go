package bot

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"krisha_kz_bot/pkg/id"
	"krisha_kz_bot/pkg/utils"

	"github.com/go-redis/redis/v9"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/pkg/errors"
)

var ErrNilHandler = errors.New("nil handler")
var ErrRedisUnmarshal = errors.New("unsupported data")

const (
	initStatesSize = 10

	DefaultPagesToScan    = 1
	DefaultSendMsgBufSize = 10
	DefaultSendMsgDelay   = "5s"
	DefaultUpdateTimeout  = 60
)

type HandlersConfig struct {
	OnWelcome   HandlerFunc // not mandatory
	OnStart     HandlerFunc // not mandatory
	OnSubscribe HandlerFunc // mandatory to process subsription outside of bot service, takes *url.URL as parameter
	OnStop      HandlerFunc // mandatory to stop subscription outsite of bot service
	OnKicked    HandlerFunc // mandatory to stop subscription outsite of bot service
	OnMessage   HandlerFunc // not mandatory
}

type Config struct {
	Token        string
	botUserName  string
	Debug        bool
	UpdateConfig tgbotapi.UpdateConfig
	SendMsgBuf   int
	SendMsgDelay time.Duration
	HandlersConfig
}

type Service struct {
	config *Config                   // service config
	api    *tgbotapi.BotAPI          // tg bot API
	outCh  chan<- tgbotapi.Chattable // oubound messages channel
	ctx    context.Context           // start context
	stop   context.CancelFunc        // stops handling of inbound updates and ounbount messages

	states map[id.Key]State       // state per each member
	urls   map[id.Key]string      // url per each subscriber
	chats  map[id.ChatID][]id.Key // subscribers per each chat id
	mx     sync.RWMutex           // controls boths, states and chatIDs hashes

	rdb *redis.Client

	handleStart HandlerFunc
	handleStop  HandlerFunc
	handleURL   HandlerFunc
}

// Creates bot service by given config.
func NewServiceFromConfig(config *Config) (*Service, error) {
	bot, err := tgbotapi.NewBotAPI(config.Token)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	*cfg = *config

	emptyHandler := func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
		return nil, nil
	}

	defOr := func(fnc HandlerFunc, def HandlerFunc) HandlerFunc {
		if fnc == nil {
			return def
		}
		return fnc
	}

	panicIfNil := func(fnc HandlerFunc) {
		if fnc == nil {
			log.Panicln(ErrNilHandler)
		}
	}

	cfg.OnWelcome = defOr(cfg.OnWelcome, emptyHandler)
	cfg.OnStart = defOr(cfg.OnStart, emptyHandler)
	cfg.OnMessage = defOr(cfg.OnMessage, emptyHandler)
	panicIfNil(cfg.OnSubscribe)
	panicIfNil(cfg.OnStop)
	panicIfNil(cfg.OnKicked)

	cfg.SendMsgBuf = utils.GraterOrEqDefOr(cfg.SendMsgBuf, DefaultSendMsgBufSize)
	cfg.SendMsgDelay = utils.GraterOrEqDefOr(cfg.SendMsgDelay, utils.ParseOrPanic[time.Duration](DefaultSendMsgDelay))

	s := &Service{
		config: cfg,
		api:    bot,
		states: make(map[id.Key]State, initStatesSize),
		urls:   make(map[id.Key]string, initStatesSize),
		chats:  make(map[id.ChatID][]id.Key, initStatesSize),
	}
	s.handleStart = withLock(s, defaultHandleStart)
	fncStop, fncPostStop := generatorDefaultHandleStop()
	s.handleStop = withPostWLock(s, fncStop, fncPostStop)
	fncURL, fncPostURL := generartorDefaultHandleURL()
	s.handleURL = withPostWLock(s, fncURL, fncPostURL)

	s.api.Debug = s.config.Debug

	s.config.botUserName = s.api.Self.UserName
	log.Printf("Authorized on account %s\n", s.api.Self.UserName)

	return s, s.setupBotCmd()
}

func (s *Service) WithRedis(rdb *redis.Client) *Service {
	s.rdb = rdb

	return s
}

// Greeting.
const (
	WelcomeText = `🖖🏻 Greeting! I am krisha.kz notification bot!
	🔎 Scanning in Almaty ⌚️ time zone.

	🕹 Commands
	/start - start bot
	/stop - stop notifications
	/url <filter> - url with query parameters, except page`
)

// Setups avvailable bot commands.
func (s *Service) setupBotCmd() error {
	cfg := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{
			Command:     "/start",
			Description: "start bot",
		},
		tgbotapi.BotCommand{
			Command:     "/stop",
			Description: "stop notifications",
		},
		// Commands does not support input parameters
		// tgbotapi.BotCommand{
		// 	Command:     "/url",
		// 	Description: "subscribe on notifications by <filter>",
		// },
	)

	if _, err := s.api.Request(cfg); err != nil {
		log.Printf("failed to setup bot commands, error %v", err)
		return err
	}

	return nil
}

// Starts serving inbound and outbound channels and blocks execution.
func (s *Service) Start(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = s.config.UpdateConfig.Timeout
	u.Offset = s.config.UpdateConfig.Offset
	u.Limit = s.config.UpdateConfig.Limit

	s.ctx, s.stop = context.WithCancel(ctx)

	// outbound messages
	s.outCh = s.getAndServOutboundChan(s.ctx, s.config)
	// inbound messages
	updates := s.api.GetUpdatesChan(u)

	// start serv inbound channel
	for {
		select {
		case <-ctx.Done():
			log.Println("stopping accepting inbound messages")
			return nil

		case update := <-updates:
			s.servInboundUpdate(&update)

		default:
			continue
		}
	}
}

// Stops serving inbound and outbound channels.
// Use for graceful shutdown.
func (s *Service) Shutdown() error {
	log.Println("shutting down bot service")
	// _, done := context.WithCancel(ctx)
	s.stop()
	// done()
	return nil
}

func (s *Service) servInboundUpdate(update *tgbotapi.Update) {
	key, resp, err := func() (id.Key, tgbotapi.Chattable, error) {
		switch {
		// serve member status change in a group
		case update.Message != nil &&
			update.Message.LeftChatMember != nil &&
			update.Message.LeftChatMember.UserName != s.config.botUserName:
			key := id.Key{
				ChatID:   id.ChatID(update.Message.Chat.ID),
				UserName: update.Message.LeftChatMember.UserName,
			}

			resp, err := s.servMemberStatusChange(update, key)
			return key, resp, err

			// serve messages
		case update.Message != nil:
			key := id.Key{
				ChatID:   id.ChatID(update.Message.Chat.ID),
				UserName: update.Message.From.UserName,
			}
			resp, err := s.servInboundMessage(update, key)

			return key, resp, err

			// serv bot status change in a chat or group
		case update.MyChatMember != nil &&
			update.MyChatMember.NewChatMember.User.UserName == s.api.Self.UserName:
			key := id.Key{
				ChatID:   id.ChatID(update.MyChatMember.Chat.ID),
				UserName: update.MyChatMember.From.UserName,
			}
			resp, err := s.servBotStatusChange(update, key)

			return key, resp, err
		}

		// unhandled message
		return id.Key{
			ChatID:   id.ChatID(update.Message.Chat.ID),
			UserName: update.Message.From.UserName,
		}, nil, nil
	}()

	if resp == nil && err != nil {
		resp = tgbotapi.NewMessage(int64(key.ChatID), err.Error())
	}

	if resp != nil {
		if e := s.SendMessage(resp); e != nil {
			log.Printf("bot failed to send message, error %v\n", e)
		}
	}
}

// Serves inboud messages.
func (s *Service) servInboundMessage(update *tgbotapi.Update, key id.Key) (tgbotapi.Chattable, error) {
	var (
		resp tgbotapi.Chattable
		err  error
	)
	switch update.Message.Text {
	case "/start", "/start@" + s.api.Self.UserName:
		resp, err = s.handleStart(update, key)

	case "/stop", "/stop@" + s.api.Self.UserName:
		resp, err = s.handleStop(update, key)
	default:
		if strings.HasPrefix(update.Message.Text, "/url") {
			resp, err = s.handleURL(update, key)
		} else {
			resp, err = s.config.OnMessage(update, key)
		}
	}

	return resp, err
}

// Serves bot status change.
func (s *Service) servBotStatusChange(update *tgbotapi.Update, key id.Key) (tgbotapi.Chattable, error) {
	memberUserName := update.MyChatMember.NewChatMember.User.UserName
	memberStatus := update.MyChatMember.NewChatMember.Status

	log.Printf("Chat member %s changed status to %s\n", memberUserName, memberStatus)

	var (
		resp tgbotapi.Chattable
		err  error
	)

	switch memberStatus {
	// bot blocked in a chat
	case "kicked":
		resp, err = withPostWLock(s,
			func(update *tgbotapi.Update, key id.Key, state getter, cfg *Config) (tgbotapi.Chattable, error) {
				_, _ = cfg.OnKicked(update, key)

				// ignore result of kick operation
				// bot would not be able to send a message to a user in a chat
				return nil, nil
			},
			func(update *tgbotapi.Update, key id.Key, state stater, cfg *Config) {
				state.setState(Default)
				// state.deleteSubscriber()
			},
		)(update, key)
	// bot removed from a group
	case "left":
		resCh := withLockV2(s,
			func(update *tgbotapi.Update, key id.Key, state stater, cfg *Config) (tgbotapi.Chattable, error) {
				_, _ = cfg.OnKicked(update, key)

				state.delState()
				// state.deleteSubscriber()

				return nil, nil
			})(update, func() []id.Key {
			keys, _ := s.wrap(key).getSubsInChat()
			return keys
		})

		// wait till all states and subscribers are unregistered
		for res := range resCh {
			// ignore results of left operation
			// bot would not be able to send a message to a user in a chat
			_, _ = res.Chattable, res.Error
		}
		resp, err = nil, nil
	// bot added to a group
	case "member":
		resp, err = tgbotapi.NewMessage(int64(key.ChatID), WelcomeText), nil
	}

	return resp, err
}

// Serves bot status change.
func (s *Service) servMemberStatusChange(update *tgbotapi.Update, key id.Key) (tgbotapi.Chattable, error) {
	resp, err := withPostWLock(s,
		func(update *tgbotapi.Update, key id.Key, state getter, cfg *Config) (tgbotapi.Chattable, error) {
			_, _ = cfg.OnKicked(update, key)

			// ignore result of kick operation
			// bot would not be able to send a message to a user in a chat
			// useless to send a message to a group for a user when he has left it
			return nil, nil
		},
		func(update *tgbotapi.Update, key id.Key, state stater, cfg *Config) {
			state.setState(Default)
			// state.deleteSubscriber()
		},
	)(update, key)

	return resp, err
}

// Returns channel for outbound messages.
func (s *Service) getAndServOutboundChan(ctx context.Context, cfg *Config) chan<- tgbotapi.Chattable {
	ch := make(chan tgbotapi.Chattable, cfg.SendMsgBuf)

	// serv outbound channel
	go func(ch chan tgbotapi.Chattable, api *tgbotapi.BotAPI) {
		defer close(ch)

		for {
			select {
			case <-ctx.Done():
				log.Printf("stopping accepting outbound messages\n")
				return

			case msg := <-ch:
				if _, err := api.Send(msg); err != nil {
					log.Printf("failed to send message, error %v\n", err)
				}

			default:
				time.Sleep(cfg.SendMsgDelay)
			}
		}
	}(ch, s.api)

	return ch
}

// Sends a message through a channel.
func (s *Service) SendMessage(c tgbotapi.Chattable) error {
	// _, err = s.api.Send(c)
	s.outCh <- c
	return nil
}

// Clear internal cache.
func (s *Service) Clean() {
	// TODO use withLock
	s.mx.Lock()
	defer s.mx.Unlock()

	// cleansing of bot cache
	for key, state := range s.states {
		if state == Default {
			log.Printf("Cleansing bot cache: %v\n", key)

			// delete from subscribers hash by key
			s.wrap(key).delSub()

			// delete from states hash by key
			s.wrap(key).delState()
		}
	}
}

func (s *Service) LoadFromRedis(ctx context.Context) {
	s.load(ctx)
}
