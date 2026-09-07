package flowview

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codeflow/internal/semantic"
)

// EventHub manages a durable sequence and a bounded replay ring for the live
// workspace stream. A slow subscriber is disconnected with an explicit
// replay boundary instead of silently losing an event.
type EventHub struct {
	mu             sync.Mutex
	streamID       string
	bufferCap      int
	headSeq        int
	ringBuffer     []*semantic.EventEnvelope
	subscribers    map[chan *semantic.EventEnvelope]*eventSubscriber
	persist        func([]byte) error
	approvalDedup  map[string]approvalUpdatedPublication
	ledgerPath     string
	ledgerOffset   int64
	ledgerKnown    bool
	statLedger     func(string) (os.FileInfo, error)
	readLedgerTail func(string, int64, int64) ([]byte, error)
	restoreLedger  func(string, int64, bool) error
	syncLedgerDir  func(string) error
	now            func() time.Time
	poisoned       bool
}

var errEventHubPoisoned = errors.New("event hub persistence state is unavailable")
var errEventLedgerDirectorySync = errors.New("event ledger directory sync failed")

type approvalUpdatedEventData struct {
	ApprovalEvent              semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
	ComputedBasisID            string                                  `json:"computedBasisId"`
	ValidatedAgainstSnapshotID string                                  `json:"validatedAgainstSnapshotId"`
	GenerationID               string                                  `json:"generationId"`
	CommittedAt                string                                  `json:"committedAt"`
}

type approvalUpdatedPublication struct {
	approvalEventID string
	fingerprint     string
	envelope        *semantic.EventEnvelope
}

type eventSubscriber struct {
	ch     chan *semantic.EventEnvelope
	closed bool
}

// NewEventHub creates an in-memory hub. NewDurableEventHub is used by the
// production server so sequence state survives process restart.
func NewEventHub(streamID string, bufferCap int) *EventHub {
	return newEventHub(streamID, bufferCap, nil)
}

func newEventHub(streamID string, bufferCap int, persist func([]byte) error) *EventHub {
	if bufferCap <= 0 {
		bufferCap = 100
	}
	if streamID == "" {
		streamID = "live-comprehension-stream"
	}
	return &EventHub{streamID: streamID, bufferCap: bufferCap, ringBuffer: make([]*semantic.EventEnvelope, 0, bufferCap), subscribers: make(map[chan *semantic.EventEnvelope]*eventSubscriber), persist: persist, approvalDedup: make(map[string]approvalUpdatedPublication), statLedger: os.Stat, readLedgerTail: readEventLedgerTail, restoreLedger: restoreEventLedgerFile, syncLedgerDir: syncEventLedgerDirectory, now: time.Now}
}

// NewDurableEventHub opens an append-only event ledger and rebuilds the replay
// ring and sequence from it. Invalid trailing records are skipped while prior
// complete events remain replayable.
func NewDurableEventHub(streamID string, bufferCap int, ledgerPath string) (*EventHub, error) {
	if ledgerPath == "" {
		return nil, fmt.Errorf("event ledger path is required")
	}
	h := newEventHub(streamID, bufferCap, nil)
	h.ledgerPath = ledgerPath
	f, err := os.OpenFile(ledgerPath, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			h.ledgerKnown = true
			h.persist = appendEventLedger(ledgerPath)
			return h, nil
		}
		return nil, fmt.Errorf("open event ledger: %w", err)
	}
	seenIDs := make(map[string]bool)
	expectedSequence := 1
	reader := bufio.NewReaderSize(f, 64*1024)
	lastCompleteOffset := int64(0)
	for index := 1; ; index++ {
		line, complete, nextOffset, readErr := readLedgerRecord(reader, lastCompleteOffset)
		if readErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("read event ledger record %d: %w", index, readErr)
		}
		if line == nil {
			break
		}
		if !complete {
			// A crash may leave one unterminated record. Truncate it before
			// enabling append, otherwise the next event would be concatenated
			// onto corrupt bytes and a later restart would fail.
			if err := f.Truncate(lastCompleteOffset); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("truncate partial event ledger record: %w", err)
			}
			if err := f.Sync(); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("sync truncated event ledger: %w", err)
			}
			break
		}
		lastCompleteOffset = nextOffset
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !utf8.Valid(line) {
			_ = f.Close()
			return nil, fmt.Errorf("event ledger contains invalid UTF-8 in record %d", index)
		}
		env, err := decodePersistedEventEnvelope(line)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("event ledger contains malformed record %d: %w", index, err)
		}
		if err := validatePersistedEventEnvelope(&env, h.streamID); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("event ledger record %d is invalid: %w", index, err)
		}
		if env.Sequence != expectedSequence || seenIDs[env.EventID] {
			_ = f.Close()
			return nil, fmt.Errorf("event ledger record %d is out of order, duplicated, or has invalid identity", index)
		}
		if env.EventType == "approval.updated" {
			if err := rejectDuplicateJSONKeys(line); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("event ledger record %d has malformed approval publication: %w", index, err)
			}
			publication, err := approvalUpdatedPublicationFromEnvelope(&env)
			if err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("event ledger record %d has invalid approval publication: %w", index, err)
			}
			if _, exists := h.approvalDedup[publication.approvalEventID]; exists {
				_ = f.Close()
				return nil, fmt.Errorf("event ledger record %d contains a duplicate approval source", index)
			}
			h.approvalDedup[publication.approvalEventID] = publication
		}
		seenIDs[env.EventID] = true
		envCopy := env
		h.appendRingLocked(&envCopy)
		h.headSeq = env.Sequence
		expectedSequence++
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close event ledger: %w", err)
	}
	h.ledgerOffset = lastCompleteOffset
	h.ledgerKnown = true
	h.persist = appendEventLedger(ledgerPath)
	return h, nil
}

func decodePersistedEventEnvelope(line []byte) (semantic.EventEnvelope, error) {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(line))
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		if err == nil {
			err = fmt.Errorf("event envelope must be an object")
		}
		return semantic.EventEnvelope{}, err
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return semantic.EventEnvelope{}, err
	}
	occurredAtRaw, ok := raw["occurredAt"]
	if !ok {
		return semantic.EventEnvelope{}, fmt.Errorf("event envelope occurredAt is required")
	}
	var occurredAtText string
	if err := json.Unmarshal(occurredAtRaw, &occurredAtText); err != nil {
		return semantic.EventEnvelope{}, fmt.Errorf("event envelope occurredAt must be a string")
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, occurredAtText)
	if err != nil || occurredAt.Location() != time.UTC || occurredAt.Format(time.RFC3339Nano) != occurredAtText {
		return semantic.EventEnvelope{}, fmt.Errorf("event envelope occurredAt is not canonical UTC")
	}

	var env semantic.EventEnvelope
	decoder = json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		return semantic.EventEnvelope{}, fmt.Errorf("event envelope contains an unknown field or invalid shape")
	}
	if err := ensureJSONDecoderEOF(decoder); err != nil {
		return semantic.EventEnvelope{}, err
	}
	return env, nil
}

func ensureJSONDecoderEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON document")
	}
	return err
}

const maxEventRecordBytes = 1 << 20

// readLedgerRecord returns one bounded record. It never accumulates more than
// maxEventRecordBytes, even when a corrupt line has no newline terminator.
func readLedgerRecord(reader *bufio.Reader, previousOffset int64) ([]byte, bool, int64, error) {
	var record []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(record)+len(chunk) > maxEventRecordBytes {
			return nil, false, previousOffset, fmt.Errorf("event record exceeds %d bytes", maxEventRecordBytes)
		}
		record = append(record, chunk...)
		if err == nil {
			return record, true, previousOffset + int64(len(record)), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			if len(record) == 0 {
				return nil, true, previousOffset, nil
			}
			return record, false, previousOffset, nil
		}
		return nil, false, previousOffset, err
	}
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON document")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON object key")
				}
				seen[key] = struct{}{}
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("object is not closed")
			}
		case '[':
			for decoder.More() {
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("array is not closed")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	return nil
}

func appendEventLedger(path string) func([]byte) error {
	return func(data []byte) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		_, statErr := os.Stat(path)
		created := errors.Is(statErr, os.ErrNotExist)
		if statErr != nil && !created {
			return statErr
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := make([]byte, len(data)+1)
		copy(record, data)
		record[len(record)-1] = '\n'
		if n, err := f.Write(record); err != nil {
			_ = f.Close()
			return err
		} else if n != len(record) {
			_ = f.Close()
			return io.ErrShortWrite
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if created {
			if err := syncEventLedgerDirectory(filepath.Dir(path)); err != nil {
				return fmt.Errorf("%w: %v", errEventLedgerDirectorySync, err)
			}
		}
		return nil
	}
}

func syncEventLedgerDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

type eventLedgerBeforeAppend struct {
	path    string
	offset  int64
	existed bool
}

func readEventLedgerTail(path string, offset, length int64) ([]byte, error) {
	if length < 0 || length > maxEventRecordBytes {
		return nil, fmt.Errorf("event ledger tail is out of bounds")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data := make([]byte, int(length))
	read, err := file.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if int64(read) != length {
		return data[:read], io.ErrUnexpectedEOF
	}
	return data, nil
}

func restoreEventLedgerFile(path string, offset int64, existed bool) error {
	if !existed {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Truncate(offset); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (h *EventHub) ledgerBeforeAppendLocked() (*eventLedgerBeforeAppend, error) {
	if h.ledgerPath == "" {
		return nil, nil
	}
	statLedger := h.statLedger
	if statLedger == nil {
		statLedger = os.Stat
	}
	info, err := statLedger(h.ledgerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if h.ledgerKnown && h.ledgerOffset != 0 {
				return nil, fmt.Errorf("event ledger disappeared after durable publication")
			}
			h.ledgerKnown = true
			h.ledgerOffset = 0
			return &eventLedgerBeforeAppend{path: h.ledgerPath}, nil
		}
		return nil, fmt.Errorf("stat event ledger before append: %w", err)
	}
	if !h.ledgerKnown {
		h.ledgerOffset = info.Size()
		h.ledgerKnown = true
	}
	if info.Size() != h.ledgerOffset {
		return nil, fmt.Errorf("event ledger length changed unexpectedly")
	}
	return &eventLedgerBeforeAppend{path: h.ledgerPath, offset: h.ledgerOffset, existed: true}, nil
}

// refreshLedgerOffsetLocked records the durable ledger high-water after an
// atomic commit callback. Such callbacks may append the event through another
// storage owner, so the next EventHub append must begin at the callback's
// resulting length rather than the length observed before it ran.
func (h *EventHub) refreshLedgerOffsetLocked() error {
	if h.ledgerPath == "" {
		return nil
	}
	statLedger := h.statLedger
	if statLedger == nil {
		statLedger = os.Stat
	}
	info, err := statLedger(h.ledgerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if h.ledgerKnown && h.ledgerOffset != 0 {
				return fmt.Errorf("event ledger disappeared after atomic commit")
			}
			h.ledgerOffset = 0
			h.ledgerKnown = true
			return nil
		}
		return fmt.Errorf("stat event ledger after atomic commit: %w", err)
	}
	if h.ledgerKnown && info.Size() < h.ledgerOffset {
		return fmt.Errorf("event ledger shrank after atomic commit")
	}
	h.ledgerOffset = info.Size()
	h.ledgerKnown = true
	return nil
}

func (h *EventHub) syncLedgerDirectoryLocked(path string) error {
	syncDir := h.syncLedgerDir
	if syncDir == nil {
		syncDir = syncEventLedgerDirectory
	}
	return syncDir(path)
}

func (h *EventHub) persistEncodedLocked(data []byte) (bool, error) {
	if h.persist == nil {
		return false, nil
	}
	before, err := h.ledgerBeforeAppendLocked()
	if err != nil {
		return false, err
	}
	persistErr := h.persist(data)
	if persistErr == nil {
		if before != nil {
			if !before.existed {
				if err := h.syncLedgerDirectoryLocked(filepath.Dir(before.path)); err != nil {
					h.poisoned = true
					return false, errors.Join(err, errEventHubPoisoned)
				}
			}
			h.ledgerOffset = before.offset + int64(len(data)) + 1
			h.ledgerKnown = true
		}
		return false, nil
	}
	if errors.Is(persistErr, errEventLedgerDirectorySync) {
		h.poisoned = true
		return false, errors.Join(persistErr, errEventHubPoisoned)
	}
	if before == nil {
		// A legacy in-memory test seam without a managed ledger cannot prove
		// whether its callback partially persisted bytes. Do not permit a
		// subsequent retry to append into an unknown state.
		h.poisoned = true
		return false, errors.Join(persistErr, errEventHubPoisoned)
	}
	durable, reconcileErr := h.reconcileLedgerAppendLocked(before, data)
	if durable {
		return true, nil
	}
	if reconcileErr != nil {
		h.poisoned = true
		return false, errors.Join(persistErr, reconcileErr, errEventHubPoisoned)
	}
	return false, persistErr
}

func (h *EventHub) ensureHealthyLocked() error {
	if h.poisoned {
		return errEventHubPoisoned
	}
	return nil
}

func (h *EventHub) reconcileLedgerAppendLocked(before *eventLedgerBeforeAppend, data []byte) (bool, error) {
	statLedger := h.statLedger
	if statLedger == nil {
		statLedger = os.Stat
	}
	info, err := statLedger(before.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !before.existed {
			return false, nil
		}
		return false, fmt.Errorf("cannot inspect event ledger after append failure")
	}
	recordLength := int64(len(data)) + 1
	if info.Size() < before.offset {
		return false, fmt.Errorf("event ledger shrank during append")
	}
	delta := info.Size() - before.offset
	if delta == 0 {
		if before.existed {
			return false, nil
		}
		return false, h.restoreAndVerifyLedgerLocked(before)
	}
	if delta == recordLength {
		tail, err := h.readLedgerTailBytes(before.path, before.offset, delta)
		if err != nil {
			return false, fmt.Errorf("cannot inspect event ledger append")
		}
		if bytes.Equal(tail, append(append([]byte(nil), data...), '\n')) {
			if !before.existed {
				if err := h.syncLedgerDirectoryLocked(filepath.Dir(before.path)); err != nil {
					return false, fmt.Errorf("sync event ledger directory after append: %w", err)
				}
			}
			h.ledgerOffset = before.offset + recordLength
			h.ledgerKnown = true
			return true, nil
		}
		return false, fmt.Errorf("event ledger append outcome is ambiguous")
	}
	if delta > 0 && delta < recordLength {
		tail, err := h.readLedgerTailBytes(before.path, before.offset, delta)
		if err != nil {
			return false, fmt.Errorf("cannot inspect event ledger append")
		}
		record := append(append([]byte(nil), data...), '\n')
		if bytes.Equal(tail, record[:delta]) {
			return false, h.restoreAndVerifyLedgerLocked(before)
		}
	}
	return false, fmt.Errorf("event ledger append outcome is ambiguous")
}

func (h *EventHub) readLedgerTailBytes(path string, offset, length int64) ([]byte, error) {
	readTail := h.readLedgerTail
	if readTail == nil {
		readTail = readEventLedgerTail
	}
	return readTail(path, offset, length)
}

func (h *EventHub) restoreAndVerifyLedgerLocked(before *eventLedgerBeforeAppend) error {
	restoreLedger := h.restoreLedger
	if restoreLedger == nil {
		restoreLedger = restoreEventLedgerFile
	}
	if err := restoreLedger(before.path, before.offset, before.existed); err != nil {
		return fmt.Errorf("restore event ledger after append failure")
	}
	if !before.existed {
		if err := h.syncLedgerDirectoryLocked(filepath.Dir(before.path)); err != nil {
			return fmt.Errorf("sync event ledger directory after restoration: %w", err)
		}
	}
	statLedger := h.statLedger
	if statLedger == nil {
		statLedger = os.Stat
	}
	info, err := statLedger(before.path)
	if !before.existed && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !before.existed || info.Size() != before.offset {
		return fmt.Errorf("event ledger restoration could not be verified")
	}
	return nil
}

var canonicalEventTypes = map[string]struct{}{
	"activity.updated":     {},
	"generation.published": {},
	"generation.gap":       {},
	"approval.updated":     {},
	"snapshot_sync":        {},
}

func requireEventIdentity(name string, value *string) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fmt.Errorf("%s identity is required", name)
	}
	return nil
}

func isCanonicalBasisID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch >= 'a' && ch <= 'f' {
			continue
		}
		return false
	}
	return true
}

func validateEventData(data any) (map[string]json.RawMessage, error) {
	if data == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("event data is not JSON: %w", err)
	}
	var object map[string]json.RawMessage
	if string(encoded) == "null" || json.Unmarshal(encoded, &object) != nil || object == nil {
		return nil, fmt.Errorf("event data must be an object")
	}
	return object, nil
}

// validateCanonicalEventEnvelope checks the v2 envelope and the identity
// rules that are specific to each event type. It runs before a sequence is
// made visible so rejected input cannot create a gap in the stream.
func validateCanonicalEventEnvelope(env *semantic.EventEnvelope) error {
	if env == nil {
		return fmt.Errorf("event envelope is nil")
	}
	if env.SchemaID != semantic.EventEnvelopeSchemaID || env.SchemaVersion != semantic.SemanticSchemaVersion {
		return fmt.Errorf("event envelope schema identity is not canonical")
	}
	if strings.TrimSpace(env.StreamID) == "" {
		return fmt.Errorf("event envelope stream identity is required")
	}
	if env.Sequence < 1 {
		return fmt.Errorf("event envelope sequence must be positive")
	}
	if strings.TrimSpace(env.EventID) == "" {
		return fmt.Errorf("event envelope event identity is required")
	}
	if env.OccurredAt.IsZero() {
		return fmt.Errorf("event envelope occurredAt is required")
	}
	if _, ok := canonicalEventTypes[env.EventType]; !ok {
		return fmt.Errorf("event envelope type %q is not canonical", env.EventType)
	}
	if env.ComputedBasisID != nil && !isCanonicalBasisID(*env.ComputedBasisID) {
		return fmt.Errorf("event envelope computed basis identity is not a canonical digest")
	}
	if env.ValidatedAgainstSnapshotID != nil && strings.TrimSpace(*env.ValidatedAgainstSnapshotID) == "" {
		return fmt.Errorf("event envelope snapshot identity must not be empty")
	}
	if env.GenerationID != nil && strings.TrimSpace(*env.GenerationID) == "" {
		return fmt.Errorf("event envelope generation identity must not be empty")
	}
	if env.PayloadRef != nil && (strings.TrimSpace(*env.PayloadRef) == "" || !strings.HasPrefix(*env.PayloadRef, "cas:")) {
		return fmt.Errorf("event envelope payload reference is not immutable")
	}
	data, err := validateEventData(env.Data)
	if err != nil {
		return err
	}
	switch env.EventType {
	case "generation.published":
		for name, value := range map[string]*string{
			"computed basis":     env.ComputedBasisID,
			"validated snapshot": env.ValidatedAgainstSnapshotID,
			"generation":         env.GenerationID,
		} {
			if err := requireEventIdentity(name, value); err != nil {
				return err
			}
		}
	case "generation.gap":
		if err := requireEventIdentity("computed basis", env.ComputedBasisID); err != nil {
			return err
		}
		if err := requireEventIdentity("validated snapshot", env.ValidatedAgainstSnapshotID); err != nil {
			return err
		}
		if env.GenerationID != nil {
			return fmt.Errorf("generation gap must not carry a generation identity")
		}
	case "activity.updated":
		if rawActivity, ok := data["activity"]; ok {
			var activity string
			if err := json.Unmarshal(rawActivity, &activity); err != nil {
				return fmt.Errorf("activity.updated activity is not a string: %w", err)
			}
			if activity != "idle" {
				if err := requireEventIdentity("validated snapshot", env.ValidatedAgainstSnapshotID); err != nil {
					return err
				}
			}
		}
	case "snapshot_sync":
		if env.ComputedBasisID != nil || env.ValidatedAgainstSnapshotID != nil || env.GenerationID != nil || env.PayloadRef != nil {
			return fmt.Errorf("snapshot sync is transport-only and cannot carry publication identities")
		}
	}
	return nil
}

func validatePersistedEventEnvelope(env *semantic.EventEnvelope, streamID string) error {
	if err := validateCanonicalEventEnvelope(env); err != nil {
		return err
	}
	if env.StreamID != streamID {
		return fmt.Errorf("event envelope stream identity %q does not match %q", env.StreamID, streamID)
	}
	if env.EventType == "snapshot_sync" {
		return fmt.Errorf("snapshot sync is a transport envelope and cannot be persisted")
	}
	return nil
}

const approvalUpdatedMaxVersion int64 = 1_000_000_000

var approvalUpdatedDecisions = map[string]struct{}{
	"approve":           {},
	"edit_then_approve": {},
	"reject":            {},
	"revoke":            {},
	"supersede":         {},
}

func validateApprovalUpdatedCommittedEvent(event semantic.ApprovalOutboxCommittedEventV1) error {
	for name, value := range map[string]string{
		"approval event": event.EventID,
		"approval":       event.ApprovalID,
		"aggregate":      event.AggregateID,
		"workspace":      event.WorkspaceID,
	} {
		if !semantic.ValidApprovalLifecycleID(value) {
			return fmt.Errorf("approval.updated %s identity is invalid", name)
		}
	}
	if event.AggregateVersion < 1 || event.AggregateVersion > approvalUpdatedMaxVersion {
		return fmt.Errorf("approval.updated aggregate version is invalid")
	}
	if _, ok := approvalUpdatedDecisions[event.Decision]; !ok {
		return fmt.Errorf("approval.updated decision is invalid")
	}
	if len(event.PayloadDigest) != len("sha256:")+64 || !strings.HasPrefix(event.PayloadDigest, "sha256:") {
		return fmt.Errorf("approval.updated payload digest is invalid")
	}
	for _, ch := range event.PayloadDigest[len("sha256:"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return fmt.Errorf("approval.updated payload digest is invalid")
		}
	}
	return nil
}

func validateApprovalUpdatedData(data any) (approvalUpdatedEventData, string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data is not JSON: %w", err)
	}
	var raw map[string]json.RawMessage
	if string(encoded) == "null" || json.Unmarshal(encoded, &raw) != nil || raw == nil {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data must be an object")
	}
	for _, key := range []string{"approvalEvent", "computedBasisId", "validatedAgainstSnapshotId", "generationId", "committedAt"} {
		if _, ok := raw[key]; !ok {
			return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data is missing %s", key)
		}
	}
	if len(raw) != 5 {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data has unknown fields")
	}
	var decoded approvalUpdatedEventData
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data shape is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data has trailing content")
	}
	var approvalRaw map[string]json.RawMessage
	if json.Unmarshal(raw["approvalEvent"], &approvalRaw) != nil || approvalRaw == nil {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated committed event must be an object")
	}
	for _, key := range []string{"eventId", "approvalId", "aggregateId", "aggregateVersion", "workspaceId", "decision", "payloadDigest"} {
		if _, ok := approvalRaw[key]; !ok {
			return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated committed event is missing %s", key)
		}
	}
	if len(approvalRaw) != 7 {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated committed event has unknown fields")
	}
	if err := validateApprovalUpdatedCommittedEvent(decoded.ApprovalEvent); err != nil {
		return approvalUpdatedEventData{}, "", err
	}
	if _, err := parseCanonicalApprovalUpdatedTime(decoded.CommittedAt); err != nil {
		return approvalUpdatedEventData{}, "", err
	}
	if !isCanonicalBasisID(decoded.ComputedBasisID) || !semantic.ValidApprovalLifecycleID(decoded.ValidatedAgainstSnapshotID) || !semantic.ValidApprovalLifecycleID(decoded.GenerationID) {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated publication identity is invalid")
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return approvalUpdatedEventData{}, "", fmt.Errorf("approval.updated data cannot be canonicalized")
	}
	digest := sha256.Sum256(canonical)
	return decoded, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func approvalUpdatedPublicationFromEnvelope(env *semantic.EventEnvelope) (approvalUpdatedPublication, error) {
	if env == nil || env.EventType != "approval.updated" {
		return approvalUpdatedPublication{}, fmt.Errorf("approval.updated envelope type is required")
	}
	if env.ComputedBasisID == nil || env.ValidatedAgainstSnapshotID == nil || env.GenerationID == nil || env.PayloadRef != nil {
		return approvalUpdatedPublication{}, fmt.Errorf("approval.updated envelope identities are invalid")
	}
	data, fingerprint, err := validateApprovalUpdatedData(env.Data)
	if err != nil {
		return approvalUpdatedPublication{}, err
	}
	if *env.ComputedBasisID != data.ComputedBasisID || *env.ValidatedAgainstSnapshotID != data.ValidatedAgainstSnapshotID || *env.GenerationID != data.GenerationID {
		return approvalUpdatedPublication{}, fmt.Errorf("approval.updated envelope and data identities differ")
	}
	if env.OccurredAt.Location() != time.UTC || env.OccurredAt.Format(time.RFC3339Nano) != env.OccurredAt.UTC().Format(time.RFC3339Nano) {
		return approvalUpdatedPublication{}, fmt.Errorf("approval.updated occurredAt is not canonical UTC")
	}
	committedAt, err := parseCanonicalApprovalUpdatedTime(data.CommittedAt)
	if err != nil {
		return approvalUpdatedPublication{}, err
	}
	if env.OccurredAt.Before(committedAt) {
		return approvalUpdatedPublication{}, fmt.Errorf("approval.updated occurredAt precedes committedAt")
	}
	return approvalUpdatedPublication{approvalEventID: data.ApprovalEvent.EventID, fingerprint: fingerprint, envelope: cloneEventEnvelope(env)}, nil
}

func parseCanonicalApprovalUpdatedTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("approval.updated committedAt is not canonical UTC")
	}
	return parsed, nil
}

func cloneEventEnvelope(env *semantic.EventEnvelope) *semantic.EventEnvelope {
	if env == nil {
		return nil
	}
	clone := *env
	if env.ComputedBasisID != nil {
		value := *env.ComputedBasisID
		clone.ComputedBasisID = &value
	}
	if env.ValidatedAgainstSnapshotID != nil {
		value := *env.ValidatedAgainstSnapshotID
		clone.ValidatedAgainstSnapshotID = &value
	}
	if env.GenerationID != nil {
		value := *env.GenerationID
		clone.GenerationID = &value
	}
	if env.PayloadRef != nil {
		value := *env.PayloadRef
		clone.PayloadRef = &value
	}
	if env.Data != nil {
		if encoded, err := json.Marshal(env.Data); err == nil {
			var data any
			if json.Unmarshal(encoded, &data) == nil {
				clone.Data = data
			} else {
				clone.Data = nil
			}
		} else {
			clone.Data = nil
		}
	}
	return &clone
}

// Publish creates, durably records, stores, and broadcasts an event. A
// persistence failure returns nil and does not advance the visible sequence.
func (h *EventHub) Publish(eventType string, data any, optBasisID, optSnapshotID, optGenID *string) *semantic.EventEnvelope {
	env, _ := h.PublishChecked(eventType, data, optBasisID, optSnapshotID, optGenID)
	return env
}

// PublishChecked is the error-returning production seam for event persistence.
func (h *EventHub) PublishChecked(eventType string, data any, optBasisID, optSnapshotID, optGenID *string) (*semantic.EventEnvelope, error) {
	if eventType == "approval.updated" {
		return nil, fmt.Errorf("approval.updated requires the typed approval publisher")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.ensureHealthyLocked(); err != nil {
		return nil, err
	}
	env := h.newEnvelopeLocked(eventType, data, optBasisID, optSnapshotID, optGenID)
	if err := validatePersistedEventEnvelope(env, h.streamID); err != nil {
		return nil, fmt.Errorf("validate event envelope: %w", err)
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal event envelope: %w", err)
	}
	if h.persist != nil {
		if _, err := h.persistEncodedLocked(encoded); err != nil {
			return nil, fmt.Errorf("persist event envelope: %w", err)
		}
	}
	h.commitLocked(env)
	return env, nil
}

// PublishAtomic reserves the next event sequence and invokes commit while the
// reservation is held. The callback must durably commit the generation and
// event bytes as one unit. On failure neither the sequence nor subscribers are
// changed.
func (h *EventHub) PublishAtomic(eventType string, data any, optBasisID, optSnapshotID, optGenID *string, commit func(*semantic.EventEnvelope, []byte) error) (*semantic.EventEnvelope, error) {
	if eventType == "approval.updated" {
		return nil, fmt.Errorf("approval.updated requires the typed approval publisher")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.ensureHealthyLocked(); err != nil {
		return nil, err
	}
	env := h.newEnvelopeLocked(eventType, data, optBasisID, optSnapshotID, optGenID)
	if err := validatePersistedEventEnvelope(env, h.streamID); err != nil {
		return nil, fmt.Errorf("validate event envelope: %w", err)
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal event envelope: %w", err)
	}
	if commit != nil {
		if err := commit(env, encoded); err != nil {
			return nil, err
		}
		if err := h.refreshLedgerOffsetLocked(); err != nil {
			h.poisoned = true
			return nil, errors.Join(err, errEventHubPoisoned)
		}
	} else if h.persist != nil {
		if _, err := h.persistEncodedLocked(encoded); err != nil {
			return nil, fmt.Errorf("persist event envelope: %w", err)
		}
	}
	h.commitLocked(env)
	return env, nil
}

// PublishApprovalUpdated durably publishes one committed approval outbox event
// and deduplicates it by the source approval event identity. An identical
// replay returns the original envelope, including its original OccurredAt,
// without advancing the sequence or notifying subscribers. Callers must use
// this typed boundary because the generic publication methods reject
// approval.updated.
func (h *EventHub) PublishApprovalUpdated(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, computedBasisID, validatedSnapshotID, generationID string) (*semantic.EventEnvelope, error) {
	data := approvalUpdatedEventData{
		ApprovalEvent:              committed,
		ComputedBasisID:            computedBasisID,
		ValidatedAgainstSnapshotID: validatedSnapshotID,
		GenerationID:               generationID,
		CommittedAt:                committedAt,
	}
	_, fingerprint, err := validateApprovalUpdatedData(data)
	if err != nil {
		return nil, fmt.Errorf("validate approval publication: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.ensureHealthyLocked(); err != nil {
		return nil, err
	}
	if h.approvalDedup == nil {
		h.approvalDedup = make(map[string]approvalUpdatedPublication)
	}
	if existing, ok := h.approvalDedup[committed.EventID]; ok {
		if existing.fingerprint != fingerprint {
			return nil, fmt.Errorf("approval.updated source event conflicts with its durable publication")
		}
		return cloneEventEnvelope(existing.envelope), nil
	}
	committedAtTime, err := parseCanonicalApprovalUpdatedTime(committedAt)
	if err != nil {
		return nil, fmt.Errorf("validate approval publication: %w", err)
	}
	occurredAt, err := h.clockNowUTCLocked()
	if err != nil {
		return nil, err
	}
	if occurredAt.Before(committedAtTime) {
		occurredAt = committedAtTime
	}
	basisID := computedBasisID
	snapshotID := validatedSnapshotID
	genID := generationID
	env := h.newEnvelopeAtLocked("approval.updated", data, &basisID, &snapshotID, &genID, occurredAt)
	if err := validatePersistedEventEnvelope(env, h.streamID); err != nil {
		return nil, fmt.Errorf("validate approval event envelope: %w", err)
	}
	publication, err := approvalUpdatedPublicationFromEnvelope(env)
	if err != nil {
		return nil, fmt.Errorf("validate approval event publication: %w", err)
	}
	if publication.fingerprint != fingerprint || publication.approvalEventID != committed.EventID {
		return nil, fmt.Errorf("approval.updated publication identity is inconsistent")
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal approval event envelope: %w", err)
	}
	if h.persist != nil {
		if _, err := h.persistEncodedLocked(encoded); err != nil {
			return nil, fmt.Errorf("persist approval event envelope: %w", err)
		}
	}
	h.commitLocked(env)
	publication.envelope = cloneEventEnvelope(env)
	h.approvalDedup[committed.EventID] = publication
	return cloneEventEnvelope(publication.envelope), nil
}

func (h *EventHub) newEnvelopeLocked(eventType string, data any, basisID, snapshotID, genID *string) *semantic.EventEnvelope {
	occurredAt := time.Time{}
	if h.now != nil {
		occurredAt = h.now().UTC()
	}
	return h.newEnvelopeAtLocked(eventType, data, basisID, snapshotID, genID, occurredAt)
}

func (h *EventHub) newEnvelopeAtLocked(eventType string, data any, basisID, snapshotID, genID *string, occurredAt time.Time) *semantic.EventEnvelope {
	return &semantic.EventEnvelope{SchemaID: semantic.EventEnvelopeSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, StreamID: h.streamID, Sequence: h.headSeq + 1, EventID: fmt.Sprintf("event-%d", h.headSeq+1), EventType: eventType, OccurredAt: occurredAt, ComputedBasisID: basisID, ValidatedAgainstSnapshotID: snapshotID, GenerationID: genID, Data: data}
}

func (h *EventHub) clockNowUTCLocked() (time.Time, error) {
	if h.now == nil {
		return time.Time{}, fmt.Errorf("event hub clock is unavailable")
	}
	now := h.now()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("event hub clock is invalid")
	}
	return now.UTC(), nil
}

func (h *EventHub) commitLocked(env *semantic.EventEnvelope) {
	stored := cloneEventEnvelope(env)
	h.headSeq = stored.Sequence
	h.appendRingLocked(stored)
	for ch, sub := range h.subscribers {
		if sub.closed {
			continue
		}
		select {
		case ch <- cloneEventEnvelope(stored):
		default:
			// Explicitly close a lagging stream. The client reconnects with its
			// Last-Event-ID and receives replay or snapshot_sync.
			sub.closed = true
			close(ch)
			delete(h.subscribers, ch)
		}
	}
}

func (h *EventHub) appendRingLocked(env *semantic.EventEnvelope) {
	if len(h.ringBuffer) >= h.bufferCap {
		h.ringBuffer = append(h.ringBuffer[1:], env)
	} else {
		h.ringBuffer = append(h.ringBuffer, env)
	}
}

// Subscribe registers a subscriber channel and returns replayed events or a
// snapshot_sync flag when the requested event is outside the replay ring.
func (h *EventHub) Subscribe(lastEventID string) (ch chan *semantic.EventEnvelope, replay []*semantic.EventEnvelope, needsSnapshotSync bool, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := &eventSubscriber{ch: make(chan *semantic.EventEnvelope, 50)}
	ch = sub.ch
	h.subscribers[ch] = sub
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if !sub.closed {
				sub.closed = true
				close(ch)
			}
			delete(h.subscribers, ch)
		})
	}
	if lastEventID == "" {
		return ch, nil, false, cancel
	}
	foundIdx := -1
	for i, env := range h.ringBuffer {
		if env.EventID == lastEventID {
			foundIdx = i
			break
		}
	}
	if foundIdx >= 0 {
		replayed := make([]*semantic.EventEnvelope, len(h.ringBuffer)-foundIdx-1)
		for i, env := range h.ringBuffer[foundIdx+1:] {
			replayed[i] = cloneEventEnvelope(env)
		}
		return ch, replayed, false, cancel
	}
	return ch, nil, true, cancel
}

// LatestTerminalEvent returns the most recent durable publication terminal
// event. It deliberately ignores activity and transport events so a full-sync
// caller cannot surface an older gap after a later current publication. The
// returned envelope owns a deep-enough copy of its data and identities.
func (h *EventHub) LatestTerminalEvent() *semantic.EventEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.ringBuffer) - 1; i >= 0; i-- {
		env := h.ringBuffer[i]
		if env == nil || (env.EventType != "generation.published" && env.EventType != "generation.gap") {
			continue
		}
		return cloneEventEnvelope(env)
	}
	return nil
}

// SnapshotSync creates a positive-sequence recovery envelope without adding a
// second durable event to the ledger. It is a transport-level snapshot
// response, not a generation transition. Its cursor is the current durable
// head so clients can resume from the next event while preserving the ledger
// high-water mark.
func (h *EventHub) SnapshotSync(data any) *semantic.EventEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	sequence := h.headSeq
	eventID := fmt.Sprintf("event-%d", sequence)
	if len(h.ringBuffer) > 0 {
		// Durable publication IDs may be opaque. The recovery cursor must
		// identify the retained head that Subscribe can actually locate.
		eventID = h.ringBuffer[len(h.ringBuffer)-1].EventID
	}
	if sequence < 1 {
		// There is no evictable cursor before the first durable event. Keep a
		// positive transport sequence for schema consumers while making the
		// empty-ledger cursor explicit.
		sequence = 1
		eventID = "snapshot-sync-empty"
	}
	return &semantic.EventEnvelope{
		SchemaID:      semantic.EventEnvelopeSchemaID,
		SchemaVersion: semantic.SemanticSchemaVersion,
		StreamID:      h.streamID,
		Sequence:      sequence,
		EventID:       eventID,
		EventType:     "snapshot_sync",
		OccurredAt:    time.Now().UTC(),
		Data:          data,
	}
}

// FormatSSE formats an EventEnvelope as standard Server-Sent Events text.
func FormatSSE(env *semantic.EventEnvelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("event envelope is nil")
	}
	dataBytes, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal event envelope: %w", err)
	}
	return []byte(fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", env.EventID, env.EventType, string(dataBytes))), nil
}
