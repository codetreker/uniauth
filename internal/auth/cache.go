package auth

import (
	"crypto/subtle"
	"sync"
	"time"
)

type transaction struct {
	appID, provider, redirectURI, productState  string
	cookieName, browserBinding, nonce, verifier string
	operationID                                 string
	expires                                     time.Time
	size                                        int64
}

type result struct {
	appID, redirectURI, operationID, provider string
	payload                                   []byte
	expires                                   time.Time
	size                                      int64
}

type transientStore struct {
	mu                            sync.Mutex
	transactions                  map[string]transaction
	results                       map[string]result
	transactionBytes, resultBytes int64
	activeCallbacks               int
	closed                        bool
	config                        Config
}

func newTransientStore(config Config) *transientStore {
	return &transientStore{transactions: make(map[string]transaction), results: make(map[string]result), config: config}
}

func (s *transientStore) addTransaction(state string, tx transaction, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.cleanupLocked(now)
	if len(s.transactions)+s.activeCallbacks >= s.config.MaxTransactions || tx.size > s.config.MaxTransactionBytes-s.transactionBytes {
		return ErrCapacity
	}
	s.transactions[state] = tx
	s.transactionBytes += tx.size
	return nil
}

func (s *transientStore) claimTransaction(state, provider string, cookies map[string]string, now time.Time) (transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return transaction{}, ErrClosed
	}
	tx, ok := s.transactions[state]
	if !ok {
		return transaction{}, ErrUnavailable
	}
	if !now.Before(tx.expires) {
		delete(s.transactions, state)
		s.transactionBytes -= tx.size
		return transaction{}, ErrUnavailable
	}
	binding, ok := cookies[tx.cookieName]
	if tx.provider != provider || !ok || subtle.ConstantTimeCompare([]byte(binding), []byte(tx.browserBinding)) != 1 {
		return transaction{}, ErrUnavailable
	}
	// Claimed callbacks retain their reservation until provider work completes.
	// This bounds live exchanges as well as browser transactions.
	delete(s.transactions, state)
	s.activeCallbacks++
	return tx, nil
}

func (s *transientStore) finishTransaction(tx transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCallbacks--
	s.transactionBytes -= tx.size
}

func (s *transientStore) addResult(code string, entry result, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.cleanupLocked(now)
	if len(s.results) >= s.config.MaxResults || entry.size > s.config.MaxResultBytes-s.resultBytes {
		return ErrCapacity
	}
	s.results[code] = entry
	s.resultBytes += entry.size
	return nil
}

func (s *transientStore) takeResult(code, appID string, now time.Time) (result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return result{}, ErrClosed
	}
	entry, ok := s.results[code]
	if !ok {
		return result{}, ErrUnavailable
	}
	if !now.Before(entry.expires) {
		delete(s.results, code)
		s.resultBytes -= entry.size
		return result{}, ErrUnavailable
	}
	if entry.appID != appID {
		return result{}, ErrUnavailable
	}
	delete(s.results, code)
	s.resultBytes -= entry.size
	return entry, nil
}

func (s *transientStore) cleanup(now time.Time) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked(now)
}

func (s *transientStore) cleanupLocked(now time.Time) (int, int) {
	txCount, resultCount := 0, 0
	for state, tx := range s.transactions {
		if !now.Before(tx.expires) {
			delete(s.transactions, state)
			s.transactionBytes -= tx.size
			txCount++
		}
	}
	for code, entry := range s.results {
		if !now.Before(entry.expires) {
			delete(s.results, code)
			s.resultBytes -= entry.size
			resultCount++
		}
	}
	return txCount, resultCount
}

func (s *transientStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, tx := range s.transactions {
		s.transactionBytes -= tx.size
	}
	clear(s.transactions)
	clear(s.results)
	s.resultBytes = 0
}
