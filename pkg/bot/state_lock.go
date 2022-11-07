package bot

import (
	"krisha_kz_bot/pkg/id"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type (
	wrapperFunc func(
		update *tgbotapi.Update,
		key id.Key,
		state stater,
		cfg *Config,
	) (tgbotapi.Chattable, error)

	wrapperRFunc func(
		update *tgbotapi.Update,
		key id.Key,
		state getter,
		cfg *Config,
	) (tgbotapi.Chattable, error)

	wrapperPostWFunc func(
		update *tgbotapi.Update,
		key id.Key,
		state stater,
		cfg *Config,
	)
)

// Locks state in RW mode during invocation of the given fnc.
func withLock(s *Service, fnc wrapperFunc) HandlerFunc {
	return func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
		s.mx.Lock()
		defer s.mx.Unlock()

		resp, err := fnc(update, key, s.wrap(key), s.config)

		return resp, err
	}
}

// Locks state in RW mode during invocation of the given fnc for keys.
// fncGetKeys invokation is safe, because wrapped with lock.
func withLockV2(s *Service, fnc wrapperFunc) HandlerFuncV2 {
	return func(update *tgbotapi.Update, fncGetKeys func() []id.Key, params ...any) <-chan HandlerResult {
		s.mx.Lock()
		defer s.mx.Unlock()

		keys := fncGetKeys()
		resCh := make(chan HandlerResult)

		go func(keys []id.Key) {
			defer close(resCh)

			for _, key := range keys {
				resp, err := fnc(update, key, s.wrap(key), s.config)
				resCh <- HandlerResult{
					Chattable: resp,
					Error:     err,
				}
			}
		}(keys)

		return resCh
	}
}

// Locks state in R mode during invocation of the given fnc.
// Locks state in RW mode post invocation.
func withPostWLock(s *Service, fnc wrapperRFunc, fncPost wrapperPostWFunc) HandlerFunc {
	return func(update *tgbotapi.Update, key id.Key, params ...any) (tgbotapi.Chattable, error) {
		var (
			resp tgbotapi.Chattable
			err  error
		)

		if resp, err = func() (tgbotapi.Chattable, error) {
			s.mx.RLock()
			defer s.mx.RUnlock()
			return fnc(update, key, s.wrap(key), s.config)
		}(); err != nil {
			return resp, err
		}

		func() {
			s.mx.Lock()
			defer s.mx.Unlock()

			fncPost(update, key, s.wrap(key), s.config)
		}()

		// return original response
		return resp, err
	}
}
