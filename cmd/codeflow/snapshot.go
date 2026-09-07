package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"codeflow/internal/protocol"
	"codeflow/internal/workspace"
)

func captureCLISnapshot(root string, epoch int64) (protocol.Snapshot, func(), error) {
	engine, err := workspace.NewSnapshotEngine(root, epoch)
	if err != nil {
		return protocol.Snapshot{}, nil, err
	}
	head, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		return protocol.Snapshot{}, nil, err
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		return protocol.Snapshot{}, nil, err
	}
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		_ = lease.Close()
		return protocol.Snapshot{}, nil, err
	}
	return snapshot, func() { _ = lease.Close() }, nil
}

func snapshotFileFingerprint(snapshot protocol.Snapshot, paths []string) string {
	files := snapshot.Files
	if len(files) == 0 {
		files = snapshot.ContentOverlay
	}
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)
	h := sha256.New()
	for _, path := range ordered {
		content, ok := files[path]
		if !ok {
			continue
		}
		fileHash := sha256.Sum256([]byte(content))
		_, _ = fmt.Fprintf(h, "%s:%s\n", path, hex.EncodeToString(fileHash[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func snapshotContentBytes(snapshot protocol.Snapshot) map[string][]byte {
	files := snapshot.Files
	if len(files) == 0 {
		files = snapshot.ContentOverlay
	}
	out := make(map[string][]byte, len(files))
	for path, content := range files {
		out[path] = []byte(content)
	}
	return out
}
