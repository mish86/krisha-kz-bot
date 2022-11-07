package bot

import (
	"fmt"
	"krisha_kz_bot/pkg/id"
	"log"
	"net/url"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/pkg/errors"
)

type (
	HandlerFunc func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error)

	HandlerResult struct {
		Chattable tgbotapi.Chattable
		Error     error
	}
	HandlerFuncV2 func(update *tgbotapi.Update, fncGetKeys func() []id.Key, params ...any) <-chan HandlerResult
)

// Default handler on /start command.
func defaultHandleStart(update *tgbotapi.Update, key id.Key, s stater, cfg *Config) (tgbotapi.Chattable, error) {
	state, found := s.getState()

	switch {
	case found && state == Subscribed:
		text := fmt.Sprintf("@%s already registered", key.UserName)

		return tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
	case !found:
		s.setState(Default)
		// s.deleteSubscriber() // not nessesary

		_, err := cfg.OnWelcome(update, key)
		return tgbotapi.NewMessage(int64(key.ChatID), WelcomeText), err
	case found && state == Default:
		text := fmt.Sprintf("@%s, Please send a /url command with krisha.kz filter, except `page` parameter", key.UserName)

		return tgbotapi.NewMessage(int64(key.ChatID), text), nil
	}
	return cfg.OnStart(update, key)
}

// Default handler on /stop command.
func generatorDefaultHandleStop() (
	func(update *tgbotapi.Update, key id.Key, s getter, cfg *Config) (tgbotapi.Chattable, error),
	func(update *tgbotapi.Update, key id.Key, s stater, cfg *Config),
) {
	fncR := func(update *tgbotapi.Update, key id.Key, s getter, cfg *Config) (tgbotapi.Chattable, error) {
		return cfg.OnStop(update, key)
	}

	fncWPost := func(update *tgbotapi.Update, key id.Key, s stater, cfg *Config) {
		s.setState(Default)
		// s.deleteSubscriber()
	}

	return fncR, fncWPost
}

// Default handler on /url command.
func generartorDefaultHandleURL() (
	func(update *tgbotapi.Update, key id.Key, s getter, cfg *Config) (tgbotapi.Chattable, error),
	func(update *tgbotapi.Update, key id.Key, s stater, cfg *Config),
) {
	// TODO think aboout vars available in returning functions.
	var (
		state  State
		found  bool
		strURL string
	)

	fncR := func(update *tgbotapi.Update, key id.Key, s getter, cfg *Config) (tgbotapi.Chattable, error) {
		command := update.Message.Text
		command = strings.ReplaceAll(command, "/url", "")
		command = strings.ReplaceAll(command, cfg.botUserName, "")
		command = strings.TrimSpace(command)

		state, found = s.getState()
		if !found {
			state = Default
		}

		if url, resp, err := parseURL(command, key); err != nil {
			return resp, err
		} else if resp, err = cfg.OnSubscribe(update, key, *url); err != nil {
			return resp, err
		} else {
			state = Subscribed
			strURL = url.String()
			return resp, err
		}
	}

	fncWPost := func(update *tgbotapi.Update, key id.Key, s stater, cfg *Config) {
		s.setState(state)
		s.setURL(strURL)
		// s.addSubscriber()
	}

	return fncR, fncWPost
}

// Parses and validates given url value.
func parseURL(command string, key id.Key) (*url.URL, tgbotapi.Chattable, error) {
	uri, err := url.ParseRequestURI(command)
	if err != nil {
		text := fmt.Sprintf("@%s, Please enter a valid url", key.UserName)
		return uri, tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
	}
	if uri.Hostname() != "krisha.kz" {
		text := fmt.Sprintf("@%s, Please enter a filter from krisha.kz", key.UserName)
		return uri, tgbotapi.NewMessage(int64(key.ChatID), text), errors.New(text)
	}

	log.Printf("@%s requested scan of %s\n", key.UserName, uri.String())

	return uri, nil, nil
}
