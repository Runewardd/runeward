// Package ledger implements a tamper-evident, append-only audit ledger for
// runeward. Every governed action an agent takes (a shell command, a file
// read, an outbound connection, an approval decision) is recorded as an
// [Event]. Events are persisted as JSON Lines (one JSON object per line) so the
// file stays human-greppable and streaming-friendly.
//
// Tamper evidence comes from a hash chain: each record embeds the SHA-256 hash
// of the previous record (PrevHash) and its own hash (Hash) computed over a
// canonical serialization of its core fields plus PrevHash. Because every hash
// depends on the one before it, altering any historical record (or reordering,
// inserting, or deleting one) breaks the chain and is detected by [Ledger.Verify].
//
// The ledger is append-only and safe for concurrent use by multiple goroutines
// in one process. Across processes, [Open] takes an advisory file lock so only
// one runeward process may write a given ledger at a time (a second process
// pointed at the same file fails fast rather than corrupting the chain). It
// depends only on the Go standard library.
package ledger

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Event is a single audited action recorded in the ledger. Callers populate the
// descriptive fields (SessionID, Sandbox, Tool, Action, ...); the ledger assigns
// the chain fields (Seq, Time, PrevHash, Hash) on [Ledger.Append].
type Event struct {
	// Seq is the 1-based sequence number, assigned by the ledger on append.
	Seq int `json:"seq"`
	// Time is the event timestamp, set to time.Now() on append if left zero.
	Time time.Time `json:"time"`

	SessionID string `json:"session_id"`
	// Sandbox is the sandbox id the action ran in.
	Sandbox string `json:"sandbox"`
	Profile string `json:"profile"`
	// Tool is the action surface, e.g. "shell", "python", "node",
	// "file.read", "file.write", "net", "approval".
	Tool string `json:"tool"`
	// Action is the primary argument: the command, path, or hostname.
	Action string `json:"action"`
	// Args holds optional structured arguments.
	Args []string `json:"args,omitempty"`
	// Verdict is the policy decision, e.g. "allow", "deny",
	// "require-approval", or "".
	Verdict string `json:"verdict"`

	ExitCode   int   `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`

	// Meta carries freeform correlation data. Keys are sorted before hashing
	// so the record hash is independent of Go map iteration order.
	Meta map[string]string `json:"meta,omitempty"`

	// Redacted reports whether sensitive payload fields were replaced by their
	// hashes rather than stored in the clear (see [Redact]).
	Redacted bool `json:"redacted"`
	// PayloadHash is the SHA-256 hex of the canonical original payload,
	// recorded when Redacted is true so plaintext can later be proven to match.
	PayloadHash string `json:"payload_hash,omitempty"`

	// PrevHash is the Hash of the preceding record ("" for the genesis record).
	PrevHash string `json:"prev_hash"`
	// Hash is the SHA-256 hex over this record's canonical core fields + PrevHash.
	Hash string `json:"hash"`

	// KeyID identifies the signing key when the record is signed ("" when the
	// ledger is unsigned). It is the short fingerprint of the public key.
	KeyID string `json:"key_id,omitempty"`
	// Sig is the base64 ed25519 signature over this record's Hash, produced by
	// the ledger's [Signer]. Sig and KeyID are deliberately excluded from
	// hashEvent so signing does not alter the chain hash.
	Sig string `json:"sig,omitempty"`
}

// Ledger is an append-only, hash-chained audit log backed by a JSON Lines file.
// The zero value is not usable; construct one with [Open]. All methods are safe
// for concurrent use.
type Ledger struct {
	mu   sync.Mutex
	f    *os.File
	path string

	// tipHash is the Hash of the most recent record (the chain tip), "" when
	// the ledger is empty. seq is the highest sequence number written so far.
	tipHash string
	seq     int

	// signer, when set, signs every appended record's Hash with an ed25519 key.
	signer *Signer
}

// SetSigner attaches a [Signer] so that subsequent [Ledger.Append] calls sign
// each record. Passing nil disables signing. It is safe to call once after
// [Open], before concurrent appends begin.
func (l *Ledger) SetSigner(s *Signer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.signer = s
}

// Open opens (creating if necessary) the JSON Lines file at path for appending
// and scans it to recover the current chain tip (last Hash) and sequence number
// so that appends continue an existing chain rather than starting a new one.
func Open(path string) (*Ledger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ledger: open %q: %w", path, err)
	}
	// Guard against a second process appending to the same file concurrently,
	// which would interleave sequence numbers and break the hash chain.
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	l := &Ledger{f: f, path: path}

	recs, err := readAll(path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if n := len(recs); n > 0 {
		tip := recs[n-1]
		l.tipHash = tip.Hash
		l.seq = tip.Seq
	}
	return l, nil
}

// Close releases the underlying file handle.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Append assigns Seq and Time (if zero), links PrevHash to the current chain
// tip, computes Hash over the canonical form, writes the record as one JSON
// line, fsyncs it to disk, advances the tip, and returns the stored event.
func (l *Ledger) Append(ev Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil {
		return Event{}, errors.New("ledger: append on closed ledger")
	}

	ev.Seq = l.seq + 1
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	ev.PrevHash = l.tipHash
	ev.Hash = hashEvent(ev)
	if l.signer != nil {
		ev.KeyID = l.signer.KeyID()
		ev.Sig = l.signer.Sign(ev.Hash)
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return Event{}, fmt.Errorf("ledger: marshal seq %d: %w", ev.Seq, err)
	}
	line = append(line, '\n')
	if _, err := l.f.Write(line); err != nil {
		return Event{}, fmt.Errorf("ledger: write seq %d: %w", ev.Seq, err)
	}
	if err := l.f.Sync(); err != nil {
		return Event{}, fmt.Errorf("ledger: sync seq %d: %w", ev.Seq, err)
	}

	l.seq = ev.Seq
	l.tipHash = ev.Hash
	return ev, nil
}

// Records returns every event in the ledger, in write order.
func (l *Ledger) Records() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return readAll(l.path)
}

// Verify walks the ledger from the genesis record, recomputing each record's
// hash and checking its linkage to the previous record. It returns a
// descriptive error identifying the first record whose recomputed hash, prev
// hash linkage, or sequence number does not match, or nil if the chain is
// intact.
func (l *Ledger) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	recs, err := readAll(l.path)
	if err != nil {
		return err
	}

	prev := ""
	for i, ev := range recs {
		if ev.Seq != i+1 {
			return fmt.Errorf("ledger: record %d: out-of-order seq %d (expected %d)", i+1, ev.Seq, i+1)
		}
		if ev.PrevHash != prev {
			return fmt.Errorf("ledger: record seq %d: broken chain, prev_hash %q does not link to previous hash %q", ev.Seq, ev.PrevHash, prev)
		}
		want := hashEvent(ev)
		if ev.Hash != want {
			return fmt.Errorf("ledger: record seq %d: tampered, stored hash %q != recomputed %q", ev.Seq, ev.Hash, want)
		}
		prev = ev.Hash
	}
	return nil
}

// Replay returns the ordered events for a single session. It is the basis for
// deterministic replay: a caller can iterate these recorded results and
// substitute them for re-running the underlying side effects.
func (l *Ledger) Replay(sessionID string) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	recs, err := readAll(l.path)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(recs))
	for _, ev := range recs {
		if ev.SessionID == sessionID {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Export writes a pretty-printed JSON array of a session's events to w for
// compliance handoff. When sessionID is "", every event is exported.
func (l *Ledger) Export(w io.Writer, sessionID string) error {
	l.mu.Lock()
	recs, err := readAll(l.path)
	l.mu.Unlock()
	if err != nil {
		return err
	}

	events := recs
	if sessionID != "" {
		events = make([]Event, 0, len(recs))
		for _, ev := range recs {
			if ev.SessionID == sessionID {
				events = append(events, ev)
			}
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(events); err != nil {
		return fmt.Errorf("ledger: export: %w", err)
	}
	return nil
}

// Redact returns a copy of ev with sensitive payload fields replaced by their
// SHA-256 hashes and Redacted set to true. PayloadHash is set to the hash of
// the canonical original payload (Action + Args + sorted Meta), so a party who
// later learns the plaintext can prove it matches without the plaintext ever
// touching the ledger.
//
// When no sensitive values are supplied, the entire payload is redacted: Action,
// every Args entry, and every Meta value are replaced by their individual
// "sha256:<hex>" digests. When one or more sensitive values are supplied, only
// payload strings exactly equal to one of them are replaced. Tool, Verdict,
// SessionID, and other structural fields are always preserved so the chain and
// replay remain meaningful.
func Redact(ev Event, sensitive ...string) Event {
	ev.PayloadHash = hashPayload(ev)
	ev.Redacted = true

	// Copy slices/maps so we never mutate the caller's Event.
	if ev.Args != nil {
		args := make([]string, len(ev.Args))
		copy(args, ev.Args)
		ev.Args = args
	}
	if ev.Meta != nil {
		meta := make(map[string]string, len(ev.Meta))
		for k, v := range ev.Meta {
			meta[k] = v
		}
		ev.Meta = meta
	}

	if len(sensitive) == 0 {
		ev.Action = redactString(ev.Action)
		for i, a := range ev.Args {
			ev.Args[i] = redactString(a)
		}
		for k, v := range ev.Meta {
			ev.Meta[k] = redactString(v)
		}
		return ev
	}

	set := make(map[string]struct{}, len(sensitive))
	for _, s := range sensitive {
		set[s] = struct{}{}
	}
	if _, ok := set[ev.Action]; ok {
		ev.Action = redactString(ev.Action)
	}
	for i, a := range ev.Args {
		if _, ok := set[a]; ok {
			ev.Args[i] = redactString(a)
		}
	}
	for k, v := range ev.Meta {
		if _, ok := set[v]; ok {
			ev.Meta[k] = redactString(v)
		}
	}
	return ev
}

// redactString replaces a non-empty string with its "sha256:<hex>" digest,
// leaving empty strings untouched.
func redactString(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// hashEvent computes the hex-encoded SHA-256 record hash over a canonical,
// map-order-independent serialization of the event's core fields plus PrevHash.
//
// Canonical form: each field below is written to the hash as an 8-byte
// big-endian length prefix followed by its raw bytes (integers first rendered
// as base-10 ASCII, booleans as "0"/"1"). Length prefixing makes the encoding
// unambiguous, so no field value can be crafted to imitate another. The Hash
// field itself is excluded. Fields are written in this fixed order:
//
//	Seq, Time (RFC3339Nano in UTC), SessionID, Sandbox, Profile, Tool, Action,
//	len(Args), each Arg, Verdict, ExitCode, DurationMS, len(Meta),
//	each sorted Meta key then its value, Redacted, PayloadHash, PrevHash.
func hashEvent(ev Event) string {
	h := sha256.New()
	putInt(h, int64(ev.Seq))
	putStr(h, ev.Time.UTC().Format(time.RFC3339Nano))
	putStr(h, ev.SessionID)
	putStr(h, ev.Sandbox)
	putStr(h, ev.Profile)
	putStr(h, ev.Tool)
	putStr(h, ev.Action)
	putInt(h, int64(len(ev.Args)))
	for _, a := range ev.Args {
		putStr(h, a)
	}
	putStr(h, ev.Verdict)
	putInt(h, int64(ev.ExitCode))
	putInt(h, ev.DurationMS)
	putMeta(h, ev.Meta)
	putBool(h, ev.Redacted)
	putStr(h, ev.PayloadHash)
	putStr(h, ev.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// hashPayload computes the hex-encoded SHA-256 over just the sensitive payload
// (Action, Args, and sorted Meta) using the same length-prefixed encoding as
// hashEvent. It is recorded in PayloadHash when an event is redacted.
func hashPayload(ev Event) string {
	h := sha256.New()
	putStr(h, ev.Action)
	putInt(h, int64(len(ev.Args)))
	for _, a := range ev.Args {
		putStr(h, a)
	}
	putMeta(h, ev.Meta)
	return hex.EncodeToString(h.Sum(nil))
}

// putMeta writes a map's entries in sorted-key order so the digest is
// independent of Go map iteration order.
func putMeta(w io.Writer, m map[string]string) {
	putInt(w, int64(len(m)))
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		putStr(w, k)
		putStr(w, m[k])
	}
}

// putField writes an 8-byte big-endian length prefix followed by b.
func putField(w io.Writer, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(b)
}

func putStr(w io.Writer, s string) { putField(w, []byte(s)) }

func putInt(w io.Writer, i int64) { putStr(w, strconv.FormatInt(i, 10)) }

func putBool(w io.Writer, b bool) {
	if b {
		putStr(w, "1")
		return
	}
	putStr(w, "0")
}

// readAll parses every JSON Lines record from path in order. A missing file is
// treated as an empty ledger. Blank lines are skipped.
func readAll(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ledger: read %q: %w", path, err)
	}
	defer f.Close()

	var events []Event
	r := bufio.NewReader(f)
	line := 0
	for {
		line++
		b, readErr := r.ReadBytes('\n')
		trimmed := trimTrailingNewline(b)
		if len(trimmed) > 0 {
			var ev Event
			if err := json.Unmarshal(trimmed, &ev); err != nil {
				return nil, fmt.Errorf("ledger: parse %q line %d: %w", path, line, err)
			}
			events = append(events, ev)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("ledger: read %q line %d: %w", path, line, readErr)
		}
	}
	return events, nil
}

// trimTrailingNewline drops a single trailing "\n" or "\r\n" from b.
func trimTrailingNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
		if n = len(b); n > 0 && b[n-1] == '\r' {
			b = b[:n-1]
		}
	}
	return b
}
