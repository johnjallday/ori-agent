package session

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// BenchmarkCreateSession benchmarks session creation performance.
func BenchmarkCreateSession(b *testing.B) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 100)
	defer func() { _ = store.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Benchmark Session",
			AgentName: "bench-agent",
		}
		_ = store.CreateSession(ctx, sess)
	}
}

// BenchmarkGetSession benchmarks session retrieval performance.
func BenchmarkGetSession(b *testing.B) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 100)
	defer func() { _ = store.Close() }()

	// Create sessions to retrieve
	var sessionIDs []string
	for i := 0; i < 100; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Benchmark Session",
			AgentName: "bench-agent",
		}
		_ = store.CreateSession(ctx, sess)
		sessionIDs = append(sessionIDs, sess.ID)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % len(sessionIDs)
		_, _ = store.GetSession(ctx, sessionIDs[idx])
	}
}

// BenchmarkAddMessage benchmarks message addition performance.
func BenchmarkAddMessage(b *testing.B) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 100)
	defer func() { _ = store.Close() }()

	// Create a session to add messages to
	sess := &Session{
		ID:        uuid.New().String(),
		Title:     "Benchmark Session",
		AgentName: "bench-agent",
	}
	_ = store.CreateSession(ctx, sess)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := &Message{
			ID:      uuid.New().String(),
			Role:    RoleUser,
			Content: "Benchmark message content with some typical text for testing purposes.",
		}
		_ = store.AddMessage(ctx, sess.ID, msg)
	}
}

// BenchmarkListSessions benchmarks session listing with pagination.
func BenchmarkListSessions(b *testing.B) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 100)
	defer func() { _ = store.Close() }()

	// Create 100 sessions
	for i := 0; i < 100; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Benchmark Session",
			AgentName: "bench-agent",
		}
		_ = store.CreateSession(ctx, sess)
	}

	opts := &ListOptions{Limit: 20, Sort: SortByUpdatedDesc}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.ListSessions(ctx, nil, opts)
	}
}

// BenchmarkSearch benchmarks full-text search performance.
func BenchmarkSearch(b *testing.B) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 100)
	defer func() { _ = store.Close() }()

	// Create sessions with searchable content
	topics := []string{"Python", "JavaScript", "Go", "Rust", "TypeScript"}
	for i := 0; i < 100; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     topics[i%len(topics)] + " Discussion",
			AgentName: "bench-agent",
		}
		_ = store.CreateSession(ctx, sess)

		msg := &Message{
			ID:      uuid.New().String(),
			Role:    RoleUser,
			Content: "How do I use " + topics[i%len(topics)] + " for web development?",
		}
		_ = store.AddMessage(ctx, sess.ID, msg)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		topic := topics[i%len(topics)]
		_, _, _ = store.Search(ctx, topic, nil, nil)
	}
}

// TestPerformance_100Sessions tests handling of 100+ sessions.
func TestPerformance_100Sessions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50) // Cache smaller than session count
	defer func() { _ = store.Close() }()

	// Create 150 sessions
	start := time.Now()
	for i := 0; i < 150; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Performance Test Session",
			AgentName: "test-agent",
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}
	}
	createDuration := time.Since(start)
	t.Logf("Created 150 sessions in %v (avg %v/session)", createDuration, createDuration/150)

	// List sessions with pagination
	start = time.Now()
	opts := &ListOptions{Limit: 20, Sort: SortByUpdatedDesc}
	result, err := store.ListSessions(ctx, nil, opts)
	if err != nil {
		t.Fatalf("Failed to list sessions: %v", err)
	}
	listDuration := time.Since(start)
	t.Logf("Listed %d sessions (total: %d) in %v", len(result.Sessions), result.Total, listDuration)

	if result.Total != 150 {
		t.Errorf("Expected 150 sessions, got %d", result.Total)
	}

	// Performance assertions (generous limits for CI)
	if createDuration > 5*time.Second {
		t.Errorf("Session creation too slow: %v (expected < 5s)", createDuration)
	}
	if listDuration > 500*time.Millisecond {
		t.Errorf("Session listing too slow: %v (expected < 500ms)", listDuration)
	}
}

// TestPerformance_SearchWith100Sessions tests search performance with 100+ sessions.
func TestPerformance_SearchWith100Sessions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50)
	defer func() { _ = store.Close() }()

	// Create 100 sessions with messages
	topics := []string{"Python", "JavaScript", "Go", "Rust", "TypeScript", "Java", "C++", "Ruby", "Swift", "Kotlin"}
	for i := 0; i < 100; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     topics[i%len(topics)] + " Help",
			AgentName: "test-agent",
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		msg := &Message{
			ID:      uuid.New().String(),
			Role:    RoleUser,
			Content: "I need help with " + topics[i%len(topics)] + " programming language for building web applications.",
		}
		if err := store.AddMessage(ctx, sess.ID, msg); err != nil {
			t.Fatalf("Failed to add message: %v", err)
		}
	}

	// Search for a topic
	start := time.Now()
	results, total, err := store.Search(ctx, "Python", nil, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	searchDuration := time.Since(start)
	t.Logf("Search for 'Python' returned %d results (total: %d) in %v", len(results), total, searchDuration)

	if total < 10 {
		t.Errorf("Expected at least 10 Python-related results, got %d", total)
	}

	if searchDuration > 500*time.Millisecond {
		t.Errorf("Search too slow: %v (expected < 500ms)", searchDuration)
	}
}

// TestPerformance_CacheEfficiency tests cache hit rates with many sessions.
func TestPerformance_CacheEfficiency(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 20) // Small cache
	defer func() { _ = store.Close() }()

	// Create more sessions than cache size
	var sessionIDs []string
	for i := 0; i < 50; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Cache Test Session",
			AgentName: "test-agent",
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}
		sessionIDs = append(sessionIDs, sess.ID)
	}

	// Access sessions multiple times to test cache hits
	for round := 0; round < 3; round++ {
		for _, id := range sessionIDs[:10] { // Only access first 10 repeatedly
			_, err := store.GetSession(ctx, id)
			if err != nil {
				t.Fatalf("Failed to get session: %v", err)
			}
		}
	}

	// Check cache stats
	stats := store.GetCacheStats()
	t.Logf("Cache stats: size=%d, hits=%d, misses=%d, evictions=%d",
		stats.Size, stats.Hits, stats.Misses, stats.Evictions)

	// Expect some cache hits after repeated access
	if stats.Hits == 0 {
		t.Error("Expected some cache hits after repeated access")
	}
}

// TestPerformance_ConcurrentAccess tests concurrent session access.
func TestPerformance_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := NewHybridStoreWithDB(db, 50)
	defer func() { _ = store.Close() }()

	// Create sessions
	var sessionIDs []string
	for i := 0; i < 20; i++ {
		sess := &Session{
			ID:        uuid.New().String(),
			Title:     "Concurrent Test",
			AgentName: "test-agent",
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}
		sessionIDs = append(sessionIDs, sess.ID)
	}

	// Concurrent reads and writes
	done := make(chan bool, 100)
	start := time.Now()

	// Concurrent reads
	for i := 0; i < 50; i++ {
		go func(idx int) {
			id := sessionIDs[idx%len(sessionIDs)]
			_, _ = store.GetSession(ctx, id)
			done <- true
		}(i)
	}

	// Concurrent writes (messages)
	for i := 0; i < 50; i++ {
		go func(idx int) {
			id := sessionIDs[idx%len(sessionIDs)]
			msg := &Message{
				ID:      uuid.New().String(),
				Role:    RoleUser,
				Content: "Concurrent message",
			}
			_ = store.AddMessage(ctx, id, msg)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
	duration := time.Since(start)
	t.Logf("100 concurrent operations completed in %v", duration)

	if duration > 5*time.Second {
		t.Errorf("Concurrent operations too slow: %v", duration)
	}
}
