package schedule

import (
	"os"
	"path/filepath"
	"slices"
	"sync"

	"go.yaml.in/yaml/v4"
)

const tasksFile = "tasks.yaml"

// Store holds a set of scheduled tasks. FileStore persists them across
// restarts (the building block for durable schedules); MemoryStore is scoped
// to a single interactive session and dies with it.
type Store interface {
	List() ([]Task, error)
	Mutate(fn func([]Task) ([]Task, error)) error
}

type ObservableStore interface {
	Store
	Changed() <-chan struct{}
}

type taskFile struct {
	Tasks []Task `yaml:"tasks"`
}

type FileStore struct {
	dir     string
	changed chan struct{}
}

func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir, changed: make(chan struct{}, 1)}
}

// dirLocks are keyed by path, not by store, so independent FileStore values
// pointing at the same directory still serialize their read-modify-writes.
var dirLocks sync.Map

func (s *FileStore) lock() *sync.Mutex {
	mu, _ := dirLocks.LoadOrStore(filepath.Clean(s.dir), &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func (s *FileStore) Path() string {
	return filepath.Join(s.dir, tasksFile)
}

func (s *FileStore) Exists() bool {
	_, err := os.Stat(s.Path())
	return err == nil
}

func (s *FileStore) List() ([]Task, error) {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()

	return s.load()
}

func (s *FileStore) Mutate(fn func([]Task) ([]Task, error)) error {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()

	tasks, err := s.load()
	if err != nil {
		return err
	}

	updated, err := fn(tasks)
	if err != nil {
		return err
	}

	if err := s.save(updated); err != nil {
		return err
	}
	s.signalChanged()
	return nil
}

func (s *FileStore) load() ([]Task, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var f taskFile
	if err := yaml.Load(data, &f); err != nil {
		return nil, err
	}

	return f.Tasks, nil
}

func (s *FileStore) save(tasks []Task) error {
	out, err := yaml.Dump(taskFile{Tasks: tasks})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(s.dir, tasksFile+".tmp-")
	if err != nil {
		return err
	}

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}

	return os.Rename(tmp.Name(), s.Path())
}

type MemoryStore struct {
	mu      sync.Mutex
	tasks   []Task
	changed chan struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{changed: make(chan struct{}, 1)}
}

func (s *MemoryStore) List() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.tasks), nil
}

func (s *MemoryStore) Mutate(fn func([]Task) ([]Task, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated, err := fn(slices.Clone(s.tasks))
	if err != nil {
		return err
	}

	s.tasks = updated

	s.signalChanged()
	return nil
}

func (s *FileStore) signalChanged()   { signalChanged(s.changed) }
func (s *MemoryStore) signalChanged() { signalChanged(s.changed) }

func signalChanged(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Changed signals (coalesced, one buffered tick) after every successful
// Mutate, so a scheduler sleeping until the next known due time can wake up
// and re-plan when tasks are added or removed mid-sleep.
func (s *MemoryStore) Changed() <-chan struct{} {
	return s.changed
}

func (s *FileStore) Changed() <-chan struct{} {
	return s.changed
}
