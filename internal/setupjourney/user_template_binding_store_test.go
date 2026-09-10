package setupjourney

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func testUserTemplateBinding(suffix string) UserTemplateBinding {
	return UserTemplateBinding{
		UserID: "local-user", TemplateID: "music-project" + suffix,
		AttachmentID:     "uqatt_0123456789abcdef01234567",
		QuestID:          "quest_0123456789abcdef01234567",
		DefinitionDigest: digestFixtureString('a'), ExecutionDigest: digestFixtureString('b'),
	}
}

func TestUserTemplateBindingIsImmutableAndRestartDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.db")
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{Path: path, WALMode: true, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	store := NewSQLiteStore(db)
	binding := testUserTemplateBinding("")
	stored, created, err := store.ClaimUserTemplateBinding(ctx, binding)
	if err != nil || !created || !sameUserTemplateBinding(*stored, binding) {
		t.Fatalf("first claim stored=%+v created=%v err=%v", stored, created, err)
	}
	if _, created, err := store.ClaimUserTemplateBinding(ctx, binding); err != nil || created {
		t.Fatalf("idempotent claim created=%v err=%v", created, err)
	}
	otherUser := binding
	otherUser.UserID = "another-user"
	if _, created, err := store.ClaimUserTemplateBinding(ctx, otherUser); err != nil || !created {
		t.Fatalf("other-user claim created=%v err=%v", created, err)
	}
	listed, err := store.ListUserTemplateBindings(ctx, binding.TemplateID)
	if err != nil || len(listed) != 2 || listed[0].UserID != otherUser.UserID || listed[1].UserID != binding.UserID {
		t.Fatalf("template bindings=%+v err=%v", listed, err)
	}
	changed := binding
	changed.ExecutionDigest = digestFixtureString('c')
	if _, _, err := store.ClaimUserTemplateBinding(ctx, changed); !errors.Is(err, ErrUserTemplateBindingMismatch) {
		t.Fatalf("changed binding error=%v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := database.Open(ctx, &database.Config{Path: path, WALMode: true, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	found, err := NewSQLiteStore(reopened).GetUserTemplateBinding(ctx, binding.UserID, binding.TemplateID)
	if err != nil || !sameUserTemplateBinding(*found, binding) {
		t.Fatalf("reopened binding=%+v err=%v", found, err)
	}
}

func TestUserTemplateRootClaimCannotBeAdopted(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := NewSQLiteStore(db)
	first := testUserTemplateBinding("")
	claim, created, err := store.ClaimUserTemplateRoot(ctx, UserTemplateRootClaim{RootKind: "project", RootID: "project-1", UserTemplateBinding: first})
	if err != nil || !created || claim.RootID != "project-1" {
		t.Fatalf("first root claim=%+v created=%v err=%v", claim, created, err)
	}
	if _, created, err := store.ClaimUserTemplateRoot(ctx, UserTemplateRootClaim{RootKind: "project", RootID: "project-1", UserTemplateBinding: first}); err != nil || created {
		t.Fatalf("idempotent root claim created=%v err=%v", created, err)
	}
	other := first
	other.UserID = "another-user"
	other.TemplateID = "another-template"
	other.AttachmentID = "uqatt_aaaaaaaaaaaaaaaaaaaaaaaa"
	other.QuestID = "quest_aaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := store.ClaimUserTemplateRoot(ctx, UserTemplateRootClaim{RootKind: "project", RootID: "project-1", UserTemplateBinding: other}); !errors.Is(err, ErrUserTemplateRootClaimed) {
		t.Fatalf("adopt root error=%v", err)
	}
	if _, _, err := store.ClaimUserTemplateRoot(ctx, UserTemplateRootClaim{RootKind: "project", RootID: "project-foreign", UserTemplateBinding: other}); err != nil {
		t.Fatal(err)
	}
	err = store.ClaimUserTemplateRoots(ctx, []UserTemplateRootClaim{
		{RootKind: "home", RootID: "home-must-rollback", UserTemplateBinding: first},
		{RootKind: "project", RootID: "project-foreign", UserTemplateBinding: first},
	})
	if !errors.Is(err, ErrUserTemplateRootClaimed) {
		t.Fatalf("batch conflict error=%v", err)
	}
	var partial int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM setup_user_template_root_claim WHERE root_id = 'home-must-rollback'`).Scan(&partial); err != nil || partial != 0 {
		t.Fatalf("partial batch claim count=%d err=%v", partial, err)
	}
}

func TestUserTemplateBindingConcurrentFirstClaimsHaveOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	ctx := context.Background()
	firstDB, err := database.Open(ctx, &database.Config{Path: path, WALMode: true, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = firstDB.Close() }()
	secondDB, err := database.Open(ctx, &database.Config{Path: path, WALMode: true, BusyTimeout: 5000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondDB.Close() }()
	stores := []*SQLiteStore{NewSQLiteStore(firstDB), NewSQLiteStore(secondDB)}
	binding := testUserTemplateBinding("")
	var wait sync.WaitGroup
	wait.Add(2)
	created := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, store := range stores {
		go func(current *SQLiteStore) {
			defer wait.Done()
			_, wasCreated, claimErr := current.ClaimUserTemplateBinding(ctx, binding)
			created <- wasCreated
			errs <- claimErr
		}(store)
	}
	wait.Wait()
	close(created)
	close(errs)
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want 1", createdCount)
	}
}

func digestFixtureString(value byte) string {
	bytes := make([]byte, 64)
	for index := range bytes {
		bytes[index] = value
	}
	return string(bytes)
}
