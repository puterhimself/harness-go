package rlmruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrStoreUnavailable   = errors.New("runtime store unavailable")
	ErrRuntimeNotFound    = errors.New("runtime not found")
	ErrBranchNotFound     = errors.New("branch not found")
	ErrCheckpointNotFound = errors.New("checkpoint not found")
)

type Store interface {
	LoadRuntime(ctx context.Context, sessionID string) (RuntimeState, error)
	SaveRuntime(ctx context.Context, state RuntimeState) error
	LoadBranch(ctx context.Context, sessionID, branchID string) (BranchState, error)
	SaveBranch(ctx context.Context, sessionID string, branch BranchState) error
	ListBranches(ctx context.Context, sessionID string) ([]BranchState, error)
	LoadBranchHead(ctx context.Context, sessionID, branchID string) (BranchHead, error)
	PromoteBranchHead(ctx context.Context, sessionID, branchID, checkpointID string) error
	WriteCheckpoint(ctx context.Context, sessionID string, branch BranchState, runtimeState map[string]any, replay []string, completion NormalizedCompletion, trace EpisodeTrace) (Checkpoint, error)
	LoadCheckpoint(ctx context.Context, sessionID, branchID, checkpointID string) (CheckpointManifest, map[string]any, NormalizedCompletion, []string, error)
	LoadCheckpointTrace(ctx context.Context, sessionID, branchID, checkpointID string) (EpisodeTrace, error)
	AppendJournal(ctx context.Context, sessionID, branchID string, entry JournalEntry) (int64, error)
	ReadJournal(ctx context.Context, sessionID, branchID string, limit int) ([]JournalEntry, error)
	PutArtifact(ctx context.Context, sessionID, branchID, name string, value any) (ArtifactRef, error)
}

type FileStore struct {
	rootDir string
	now     func() time.Time
}

func NewFileStore(rootDir string) *FileStore {
	return &FileStore{
		rootDir: rootDir,
		now:     time.Now,
	}
}

func (s *FileStore) LoadRuntime(ctx context.Context, sessionID string) (RuntimeState, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeState{}, err
	}
	path := s.runtimePath(sessionID)
	var state RuntimeState
	if err := readJSON(path, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RuntimeState{}, ErrRuntimeNotFound
		}
		return RuntimeState{}, err
	}
	return state, nil
}

func (s *FileStore) SaveRuntime(ctx context.Context, state RuntimeState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(state.SessionID) == "" {
		return fmt.Errorf("save runtime: missing session id")
	}
	return atomicWriteJSON(s.runtimePath(state.SessionID), state)
}

func (s *FileStore) LoadBranch(ctx context.Context, sessionID, branchID string) (BranchState, error) {
	if err := ctx.Err(); err != nil {
		return BranchState{}, err
	}
	var state BranchState
	if err := readJSON(s.branchPath(sessionID, branchID), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BranchState{}, ErrBranchNotFound
		}
		return BranchState{}, err
	}
	return state, nil
}

func (s *FileStore) SaveBranch(ctx context.Context, sessionID string, branch BranchState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(branch.BranchID) == "" {
		return fmt.Errorf("save branch: missing branch id")
	}
	if branch.Status == "" {
		branch.Status = BranchStatusActive
	}
	return atomicWriteJSON(s.branchPath(sessionID, branch.BranchID), branch)
}

func (s *FileStore) ListBranches(ctx context.Context, sessionID string) ([]BranchState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base := s.branchesDir(sessionID)
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	states := make([]BranchState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		branchID := entry.Name()
		state, err := s.LoadBranch(ctx, sessionID, branchID)
		if err != nil {
			if errors.Is(err, ErrBranchNotFound) {
				continue
			}
			return nil, err
		}
		states = append(states, state)
	}

	slices.SortFunc(states, func(a, b BranchState) int {
		return strings.Compare(a.BranchID, b.BranchID)
	})

	return states, nil
}

func (s *FileStore) LoadBranchHead(ctx context.Context, sessionID, branchID string) (BranchHead, error) {
	if err := ctx.Err(); err != nil {
		return BranchHead{}, err
	}
	var head BranchHead
	if err := readJSON(s.branchHeadPath(sessionID, branchID), &head); err == nil {
		return head, nil
	}

	branch, err := s.LoadBranch(ctx, sessionID, branchID)
	if err != nil {
		if errors.Is(err, ErrBranchNotFound) {
			return BranchHead{}, ErrBranchNotFound
		}
		return BranchHead{}, err
	}
	if branch.HeadCheckpoint == "" {
		return BranchHead{}, ErrCheckpointNotFound
	}
	return BranchHead{
		BranchID:      branchID,
		CheckpointID:  branch.HeadCheckpoint,
		UpdatedAt:     s.now().UTC(),
		CheckpointDir: s.checkpointDir(sessionID, branchID, branch.HeadCheckpoint),
	}, nil
}

func (s *FileStore) PromoteBranchHead(ctx context.Context, sessionID, branchID, checkpointID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(checkpointID) == "" {
		return fmt.Errorf("promote head: missing checkpoint id")
	}

	manifestPath := s.checkpointManifestPath(sessionID, branchID, checkpointID)
	if _, err := os.Stat(manifestPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrCheckpointNotFound
		}
		return err
	}

	head := BranchHead{
		BranchID:      branchID,
		CheckpointID:  checkpointID,
		UpdatedAt:     s.now().UTC(),
		CheckpointDir: s.checkpointDir(sessionID, branchID, checkpointID),
	}
	if err := atomicWriteJSON(s.branchHeadPath(sessionID, branchID), head); err != nil {
		return err
	}

	branch, err := s.LoadBranch(ctx, sessionID, branchID)
	if err != nil {
		return err
	}
	branch.HeadCheckpoint = checkpointID
	return s.SaveBranch(ctx, sessionID, branch)
}

func (s *FileStore) WriteCheckpoint(ctx context.Context, sessionID string, branch BranchState, runtimeState map[string]any, replay []string, completion NormalizedCompletion, trace EpisodeTrace) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}

	checkpointID := uuid.NewString()
	createdAt := s.now().UTC()

	cpDir := s.checkpointDir(sessionID, branch.BranchID, checkpointID)
	manifestPath := filepath.Join(cpDir, "manifest.json")
	statePath := filepath.Join(cpDir, "state.json")
	replayPath := filepath.Join(cpDir, "replay.go")
	completionPath := filepath.Join(cpDir, "completion.json")
	tracePath := filepath.Join(cpDir, "trace.json")
	artifactRoot := s.artifactsDir(sessionID, branch.BranchID)

	manifest := CheckpointManifest{
		ID:             checkpointID,
		Version:        RuntimeSchemaVersion,
		SessionID:      sessionID,
		BranchID:       branch.BranchID,
		CreatedAt:      createdAt,
		ReplayPath:     replayPath,
		StatePath:      statePath,
		CompletionPath: completionPath,
		TracePath:      tracePath,
		ArtifactRoot:   artifactRoot,
		JournalOffset:  branch.JournalOffset,
	}

	if err := atomicWriteJSON(statePath, runtimeState); err != nil {
		return Checkpoint{}, err
	}
	if err := atomicWriteJSON(completionPath, completion); err != nil {
		return Checkpoint{}, err
	}
	if err := atomicWriteJSON(tracePath, trace); err != nil {
		return Checkpoint{}, err
	}
	if err := atomicWriteJSON(replayPath, replay); err != nil {
		return Checkpoint{}, err
	}
	if err := atomicWriteJSON(manifestPath, manifest); err != nil {
		return Checkpoint{}, err
	}

	return Checkpoint{
		ID:           checkpointID,
		BranchID:     branch.BranchID,
		CreatedAt:    createdAt,
		ManifestPath: manifestPath,
		StatePath:    statePath,
		ReplayPath:   replayPath,
		TracePath:    tracePath,
		ArtifactRoot: artifactRoot,
	}, nil
}

func (s *FileStore) LoadCheckpoint(ctx context.Context, sessionID, branchID, checkpointID string) (CheckpointManifest, map[string]any, NormalizedCompletion, []string, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, err
	}

	manifestPath := s.checkpointManifestPath(sessionID, branchID, checkpointID)
	var manifest CheckpointManifest
	if err := readJSON(manifestPath, &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, ErrCheckpointNotFound
		}
		return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, err
	}

	state := map[string]any{}
	if err := readJSON(manifest.StatePath, &state); err != nil {
		return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, err
	}

	completion := NormalizedCompletion{}
	if err := readJSON(manifest.CompletionPath, &completion); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, err
		}
	}

	replayLines, err := loadReplayBlocks(manifest.ReplayPath)
	if err != nil {
		return CheckpointManifest{}, nil, NormalizedCompletion{}, nil, err
	}

	return manifest, state, completion, replayLines, nil
}

func (s *FileStore) LoadCheckpointTrace(ctx context.Context, sessionID, branchID, checkpointID string) (EpisodeTrace, error) {
	if err := ctx.Err(); err != nil {
		return EpisodeTrace{}, err
	}

	manifestPath := s.checkpointManifestPath(sessionID, branchID, checkpointID)
	var manifest CheckpointManifest
	if err := readJSON(manifestPath, &manifest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EpisodeTrace{}, ErrCheckpointNotFound
		}
		return EpisodeTrace{}, err
	}
	if manifest.TracePath == "" {
		return EpisodeTrace{}, nil
	}

	trace := EpisodeTrace{}
	if err := readJSON(manifest.TracePath, &trace); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EpisodeTrace{}, nil
		}
		return EpisodeTrace{}, err
	}
	return trace, nil
}

func (s *FileStore) AppendJournal(ctx context.Context, sessionID, branchID string, entry JournalEntry) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	journalPath := s.journalPath(sessionID, branchID)
	if entry.Timestamp.IsZero() {
		entry.Timestamp = s.now().UTC()
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		return 0, err
	}

	file, err := os.OpenFile(journalPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return 0, err
	}

	if _, err := file.Seek(0, 0); err != nil {
		return 0, err
	}

	var lines int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}

	return lines, nil
}

func (s *FileStore) PutArtifact(ctx context.Context, sessionID, branchID, name string, value any) (ArtifactRef, error) {
	if err := ctx.Err(); err != nil {
		return ArtifactRef{}, err
	}
	if strings.TrimSpace(name) == "" {
		return ArtifactRef{}, fmt.Errorf("artifact name is required")
	}

	id := uuid.NewString()
	createdAt := s.now().UTC()
	path := filepath.Join(s.artifactsDir(sessionID, branchID), id+".json")
	if err := atomicWriteJSON(path, value); err != nil {
		return ArtifactRef{}, err
	}

	return ArtifactRef{
		ID:        id,
		Name:      name,
		Path:      path,
		CreatedAt: createdAt,
	}, nil
}

func (s *FileStore) ReadJournal(ctx context.Context, sessionID, branchID string, limit int) ([]JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	journalPath := s.journalPath(sessionID, branchID)
	file, err := os.Open(journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	entries := []JournalEntry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		entry := JournalEntry{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

func (s *FileStore) sessionsDir() string {
	return filepath.Join(s.rootDir, "sessions")
}

func (s *FileStore) sessionDir(sessionID string) string {
	return filepath.Join(s.sessionsDir(), sessionID)
}

func (s *FileStore) runtimePath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "runtime.json")
}

func (s *FileStore) branchesDir(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "branches")
}

func (s *FileStore) branchDir(sessionID, branchID string) string {
	return filepath.Join(s.branchesDir(sessionID), branchID)
}

func (s *FileStore) branchPath(sessionID, branchID string) string {
	return filepath.Join(s.branchDir(sessionID, branchID), "branch.json")
}

func (s *FileStore) branchHeadPath(sessionID, branchID string) string {
	return filepath.Join(s.branchDir(sessionID, branchID), "head.json")
}

func (s *FileStore) journalPath(sessionID, branchID string) string {
	return filepath.Join(s.branchDir(sessionID, branchID), "journal.jsonl")
}

func (s *FileStore) checkpointsDir(sessionID, branchID string) string {
	return filepath.Join(s.branchDir(sessionID, branchID), "checkpoints")
}

func (s *FileStore) checkpointDir(sessionID, branchID, checkpointID string) string {
	return filepath.Join(s.checkpointsDir(sessionID, branchID), checkpointID)
}

func (s *FileStore) checkpointManifestPath(sessionID, branchID, checkpointID string) string {
	return filepath.Join(s.checkpointDir(sessionID, branchID, checkpointID), "manifest.json")
}

func (s *FileStore) artifactsDir(sessionID, branchID string) string {
	return filepath.Join(s.branchDir(sessionID, branchID), "artifacts")
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func atomicWriteText(path string, value string) error {
	return atomicWrite(path, []byte(value))
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp-" + uuid.NewString()
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}

func loadReplayBlocks(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	var replay []string
	if err := json.Unmarshal(data, &replay); err == nil {
		return trimReplayBlocks(replay), nil
	}

	return splitLegacyReplay(string(data)), nil
}

func splitLegacyReplay(data string) []string {
	if strings.TrimSpace(data) == "" {
		return nil
	}
	lines := strings.Split(data, "\n")
	return trimReplayBlocks(lines)
}

func trimReplayBlocks(blocks []string) []string {
	if len(blocks) == 0 {
		return nil
	}
	out := append([]string(nil), blocks...)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
