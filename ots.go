package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// OpenTimestamps .ots file format constants.
// Reference: https://github.com/opentimestamps/python-opentimestamps
var otsMagic = []byte{
	0x00, 0x4f, 0x70, 0x65, 0x6e, 0x54, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x73, 0x00, 0x00, 0x50, 0x72, 0x6f,
	0x6f, 0x66, 0x00, 0xbf, 0x89, 0xe2, 0xe8, 0x84, 0xe8, 0x92, 0x94,
}

const (
	otsVersion     byte = 0x01
	otsOpSHA256    byte = 0x08
	otsOpAppend    byte = 0xf0
	otsOpPrepend   byte = 0xf1
	otsAttestation byte = 0x00
	otsFork        byte = 0xff
)

// 8-byte attestation tags.
var (
	pendingTag = []byte{0x83, 0xdf, 0xe3, 0x0d, 0x2e, 0xf9, 0x0c, 0x8e}
	bitcoinTag = []byte{0x05, 0x88, 0x96, 0x0d, 0x73, 0xd7, 0x19, 0x01}
)

var otsCalendars = []string{
	"https://a.pool.opentimestamps.org",
	"https://b.pool.opentimestamps.org",
	"https://finney.calendar.eternitywall.com",
}

// SubmitOTS POSTs the 32-byte digest to OTS calendars in order until one
// succeeds, then wraps the returned timestamp in a valid .ots file.
func SubmitOTS(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("digest must be 32 bytes, got %d", len(digest))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var errs []string
	for _, cal := range otsCalendars {
		url := strings.TrimRight(cal, "/") + "/digest"
		resp, err := client.Post(url, "application/octet-stream", bytes.NewReader(digest))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cal, err))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: read: %v", cal, err))
			continue
		}
		if resp.StatusCode != 200 {
			errs = append(errs, fmt.Sprintf("%s: status %d", cal, resp.StatusCode))
			continue
		}
		return BuildOTSFile(digest, body), nil
	}
	return nil, fmt.Errorf("all calendars failed: %s", strings.Join(errs, "; "))
}

// BuildOTSFile assembles a complete .ots file from the original digest and the
// timestamp bytes returned by a calendar.
func BuildOTSFile(digest, timestamp []byte) []byte {
	var buf bytes.Buffer
	buf.Write(otsMagic)
	buf.WriteByte(otsVersion)
	buf.WriteByte(otsOpSHA256)
	buf.Write(digest)
	buf.Write(timestamp)
	return buf.Bytes()
}

// parseOTSPending walks a .ots file to find a single pending calendar attestation.
// Returns the commitment (digest state at that branch point), the calendar URL,
// and the byte offset of the 0x00 attestation marker within proof.
// Returns an error if the proof contains forks (multi-calendar) or is already
// upgraded to a Bitcoin attestation.
func parseOTSPending(proof []byte) (commitment []byte, calURL string, attStart int, err error) {
	headerLen := len(otsMagic) + 1 + 1 + 32
	if len(proof) < headerLen {
		return nil, "", 0, fmt.Errorf("proof too short")
	}
	if !bytes.Equal(proof[:len(otsMagic)], otsMagic) {
		return nil, "", 0, fmt.Errorf("bad magic")
	}
	pos := len(otsMagic)
	if proof[pos] != otsVersion {
		return nil, "", 0, fmt.Errorf("unsupported version 0x%02x", proof[pos])
	}
	pos++
	if proof[pos] != otsOpSHA256 {
		return nil, "", 0, fmt.Errorf("unsupported file hash op 0x%02x", proof[pos])
	}
	pos++

	current := make([]byte, 32)
	copy(current, proof[pos:pos+32])
	pos += 32

	for pos < len(proof) {
		b := proof[pos]
		switch b {
		case otsFork:
			return nil, "", 0, fmt.Errorf("fork (multi-branch) proofs not supported")
		case otsAttestation:
			attAt := pos
			pos++
			if pos+8 > len(proof) {
				return nil, "", 0, fmt.Errorf("truncated attestation tag")
			}
			tag := proof[pos : pos+8]
			pos += 8
			plLen, n := binary.Uvarint(proof[pos:])
			if n <= 0 {
				return nil, "", 0, fmt.Errorf("bad attestation length")
			}
			pos += n
			if pos+int(plLen) > len(proof) {
				return nil, "", 0, fmt.Errorf("truncated attestation payload")
			}
			payload := proof[pos : pos+int(plLen)]
			pos += int(plLen)
			if bytes.Equal(tag, pendingTag) {
				urlLen, m := binary.Uvarint(payload)
				if m <= 0 || int(urlLen)+m != len(payload) {
					return nil, "", 0, fmt.Errorf("bad pending url payload")
				}
				return current, string(payload[m:]), attAt, nil
			}
			return nil, "", 0, fmt.Errorf("non-pending attestation found (tag %x)", tag)
		case otsOpSHA256:
			pos++
			h := sha256.Sum256(current)
			current = h[:]
		case otsOpAppend, otsOpPrepend:
			op := b
			pos++
			l, n := binary.Uvarint(proof[pos:])
			if n <= 0 {
				return nil, "", 0, fmt.Errorf("bad op length")
			}
			pos += n
			if pos+int(l) > len(proof) {
				return nil, "", 0, fmt.Errorf("truncated op data")
			}
			data := proof[pos : pos+int(l)]
			pos += int(l)
			if op == otsOpAppend {
				current = append(append([]byte{}, current...), data...)
			} else {
				current = append(append([]byte{}, data...), current...)
			}
		default:
			return nil, "", 0, fmt.Errorf("unsupported op 0x%02x at %d", b, pos)
		}
	}
	return nil, "", 0, fmt.Errorf("no attestation found")
}

// UpgradeOTS contacts the calendar to replace a pending attestation with an
// upgraded timestamp (ideally containing a Bitcoin attestation).
// Returns the updated proof bytes, whether any change occurred, and whether the
// upgraded proof contains a Bitcoin attestation.
func UpgradeOTS(proof []byte) (newProof []byte, changed bool, hasBTC bool, err error) {
	commitment, calURL, attStart, err := parseOTSPending(proof)
	if err != nil {
		return proof, false, false, err
	}
	endpoint := strings.TrimRight(calURL, "/") + "/timestamp/" + hex.EncodeToString(commitment)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return proof, false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return proof, false, false, nil
	}
	if resp.StatusCode != 200 {
		return proof, false, false, fmt.Errorf("calendar returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return proof, false, false, err
	}
	merged := make([]byte, 0, attStart+len(body))
	merged = append(merged, proof[:attStart]...)
	merged = append(merged, body...)
	return merged, true, bytes.Contains(merged, bitcoinTag), nil
}

// UpgradePendingOTS scans all seals with a stored proof but no bitcoin
// attestation yet, and attempts to upgrade each one. Best-effort; errors are
// logged and left for the next run.
func UpgradePendingOTS(s *Store) {
	seals, err := s.ListSeals()
	if err != nil {
		log.Printf("ots upgrade: list seals: %v", err)
		return
	}
	for _, ds := range seals {
		if !ds.HasOTSProof || ds.OTSUpgradedAt != nil {
			continue
		}
		proof, err := s.GetOTSProof(ds.Date)
		if err != nil || proof == nil {
			continue
		}
		newProof, changed, hasBTC, err := UpgradeOTS(proof)
		if err != nil {
			log.Printf("ots upgrade %s: %v", ds.Date, err)
			continue
		}
		if !changed {
			continue
		}
		if hasBTC {
			if err := s.setOTSUpgraded(ds.Date, newProof); err != nil {
				log.Printf("ots upgrade store %s: %v", ds.Date, err)
				continue
			}
			log.Printf("ots upgraded %s (bitcoin attestation)", ds.Date)
		} else {
			if err := s.SetOTSProof(ds.Date, newProof); err != nil {
				log.Printf("ots upgrade store %s: %v", ds.Date, err)
			}
		}
	}
}
