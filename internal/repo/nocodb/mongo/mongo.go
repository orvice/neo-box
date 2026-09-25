package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

const (
	connectionsCollection = "nocodb_connections"
	policiesCollection    = "nocodb_backup_policies"
	snapshotsCollection   = "nocodb_snapshots"
)

type connectionDoc struct {
	ID              string    `bson:"_id"`
	UserID          string    `bson:"user_id"`
	Name            string    `bson:"name"`
	BaseURL         string    `bson:"base_url"`
	TokenCiphertext string    `bson:"token_ciphertext"`
	CreatedAt       time.Time `bson:"created_at"`
	UpdatedAt       time.Time `bson:"updated_at"`
}

type policyDoc struct {
	ID           string    `bson:"_id"` // connection_id + "/" + base_id
	ConnectionID string    `bson:"connection_id"`
	BaseID       string    `bson:"base_id"`
	UserID       string    `bson:"user_id"`
	Enabled      bool      `bson:"enabled"`
	Cron         string    `bson:"cron"`
	Retention    int       `bson:"retention"`
	UpdatedAt    time.Time `bson:"updated_at"`
}

type snapshotDoc struct {
	ID           string               `bson:"_id"`
	UserID       string               `bson:"user_id"`
	ConnectionID string               `bson:"connection_id"`
	BaseID       string               `bson:"base_id"`
	BaseTitle    string               `bson:"base_title"`
	Status       string               `bson:"status"`
	Trigger      string               `bson:"trigger"`
	Error        string               `bson:"error,omitempty"`
	Progress     string               `bson:"progress,omitempty"`
	ObjectKey    string               `bson:"object_key,omitempty"`
	SizeBytes    int64                `bson:"size_bytes"`
	RecordCount  int64                `bson:"record_count"`
	LinkCount    int64                `bson:"link_count"`
	Tables       []repo.SnapshotTable `bson:"tables,omitempty"`
	CreatedAt    time.Time            `bson:"created_at"`
	StartedAt    time.Time            `bson:"started_at,omitempty"`
	FinishedAt   time.Time            `bson:"finished_at,omitempty"`
}

type Store struct {
	connections *mongo.Collection
	policies    *mongo.Collection
	snapshots   *mongo.Collection
}

func New(db *mongo.Database) *Store {
	return &Store{
		connections: db.Collection(connectionsCollection),
		policies:    db.Collection(policiesCollection),
		snapshots:   db.Collection(snapshotsCollection),
	}
}

func (s *Store) EnsureIndexes(ctx context.Context) error {
	if _, err := s.connections.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: 1}},
	}); err != nil {
		return fmt.Errorf("create %s index: %w", connectionsCollection, err)
	}
	if _, err := s.policies.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "connection_id", Value: 1}},
	}); err != nil {
		return fmt.Errorf("create %s index: %w", policiesCollection, err)
	}
	if _, err := s.snapshots.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "connection_id", Value: 1}, {Key: "base_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("create %s indexes: %w", snapshotsCollection, err)
	}
	return nil
}

// --- connections ---

func (s *Store) CreateConnection(ctx context.Context, c *repo.Connection) error {
	if _, err := s.connections.InsertOne(ctx, connectionToDoc(c)); err != nil {
		return fmt.Errorf("insert connection: %w", err)
	}
	return nil
}

func (s *Store) GetConnection(ctx context.Context, userID, id string) (*repo.Connection, error) {
	return s.findConnection(ctx, bson.M{"_id": id, "user_id": userID})
}

func (s *Store) GetConnectionByID(ctx context.Context, id string) (*repo.Connection, error) {
	return s.findConnection(ctx, bson.M{"_id": id})
}

func (s *Store) findConnection(ctx context.Context, filter bson.M) (*repo.Connection, error) {
	var doc connectionDoc
	if err := s.connections.FindOne(ctx, filter).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find connection: %w", err)
	}
	return connectionFromDoc(&doc), nil
}

func (s *Store) ListConnections(ctx context.Context, userID string) ([]*repo.Connection, error) {
	cur, err := s.connections.Find(ctx, bson.M{"user_id": userID}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	var docs []connectionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode connections: %w", err)
	}
	out := make([]*repo.Connection, 0, len(docs))
	for i := range docs {
		out = append(out, connectionFromDoc(&docs[i]))
	}
	return out, nil
}

func (s *Store) UpdateConnection(ctx context.Context, c *repo.Connection) error {
	res, err := s.connections.UpdateOne(ctx,
		bson.M{"_id": c.ID, "user_id": c.UserID},
		bson.M{"$set": bson.M{
			"name":             c.Name,
			"base_url":         c.BaseURL,
			"token_ciphertext": c.TokenCiphertext,
			"updated_at":       c.UpdatedAt,
		}},
	)
	if err != nil {
		return fmt.Errorf("update connection: %w", err)
	}
	if res.MatchedCount == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteConnection(ctx context.Context, userID, id string) error {
	res, err := s.connections.DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	if res.DeletedCount == 0 {
		return repo.ErrNotFound
	}
	return nil
}

// --- policies ---

func (s *Store) UpsertPolicy(ctx context.Context, p *repo.Policy) error {
	doc := policyToDoc(p)
	_, err := s.policies.ReplaceOne(ctx, bson.M{"_id": doc.ID}, doc, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("upsert policy: %w", err)
	}
	return nil
}

func (s *Store) ListPolicies(ctx context.Context, userID, connectionID string) ([]*repo.Policy, error) {
	return s.findPolicies(ctx, bson.M{"user_id": userID, "connection_id": connectionID})
}

func (s *Store) ListEnabledPolicies(ctx context.Context) ([]*repo.Policy, error) {
	return s.findPolicies(ctx, bson.M{"enabled": true})
}

func (s *Store) findPolicies(ctx context.Context, filter bson.M) ([]*repo.Policy, error) {
	cur, err := s.policies.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	var docs []policyDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode policies: %w", err)
	}
	out := make([]*repo.Policy, 0, len(docs))
	for i := range docs {
		out = append(out, policyFromDoc(&docs[i]))
	}
	return out, nil
}

func (s *Store) DeletePoliciesForConnection(ctx context.Context, connectionID string) error {
	if _, err := s.policies.DeleteMany(ctx, bson.M{"connection_id": connectionID}); err != nil {
		return fmt.Errorf("delete policies: %w", err)
	}
	return nil
}

// --- snapshots ---

func (s *Store) CreateSnapshot(ctx context.Context, snap *repo.Snapshot) error {
	if _, err := s.snapshots.InsertOne(ctx, snapshotToDoc(snap)); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

func (s *Store) GetSnapshot(ctx context.Context, userID, id string) (*repo.Snapshot, error) {
	filter := bson.M{"_id": id}
	if userID != "" {
		filter["user_id"] = userID
	}
	var doc snapshotDoc
	if err := s.snapshots.FindOne(ctx, filter).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find snapshot: %w", err)
	}
	return snapshotFromDoc(&doc), nil
}

func (s *Store) UpdateSnapshot(ctx context.Context, snap *repo.Snapshot) error {
	doc := snapshotToDoc(snap)
	res, err := s.snapshots.ReplaceOne(ctx, bson.M{"_id": doc.ID}, doc)
	if err != nil {
		return fmt.Errorf("update snapshot: %w", err)
	}
	if res.MatchedCount == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateSnapshotProgress(ctx context.Context, id, progress string) error {
	_, err := s.snapshots.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"progress": progress}})
	return err
}

func (s *Store) ListSnapshots(ctx context.Context, f repo.SnapshotFilter) ([]*repo.Snapshot, error) {
	filter := bson.M{}
	if f.UserID != "" {
		filter["user_id"] = f.UserID
	}
	if f.ConnectionID != "" {
		filter["connection_id"] = f.ConnectionID
	}
	if f.BaseID != "" {
		filter["base_id"] = f.BaseID
	}
	if f.Trigger != "" {
		filter["trigger"] = string(f.Trigger)
	}
	if f.Status != "" {
		filter["status"] = string(f.Status)
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if f.Limit > 0 {
		opts.SetLimit(int64(f.Limit))
	}
	cur, err := s.snapshots.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var docs []snapshotDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode snapshots: %w", err)
	}
	out := make([]*repo.Snapshot, 0, len(docs))
	for i := range docs {
		out = append(out, snapshotFromDoc(&docs[i]))
	}
	return out, nil
}

func (s *Store) DeleteSnapshot(ctx context.Context, id string) error {
	if _, err := s.snapshots.DeleteOne(ctx, bson.M{"_id": id}); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

func (s *Store) FailUnfinishedSnapshots(ctx context.Context, reason string, at time.Time) (int64, error) {
	res, err := s.snapshots.UpdateMany(ctx,
		bson.M{"status": bson.M{"$in": bson.A{string(repo.StatusPending), string(repo.StatusRunning)}}},
		bson.M{"$set": bson.M{"status": string(repo.StatusFailed), "error": reason, "finished_at": at, "progress": ""}},
	)
	if err != nil {
		return 0, fmt.Errorf("fail unfinished snapshots: %w", err)
	}
	return res.ModifiedCount, nil
}

// --- conversions ---

func connectionToDoc(c *repo.Connection) connectionDoc {
	return connectionDoc{
		ID: c.ID, UserID: c.UserID, Name: c.Name, BaseURL: c.BaseURL,
		TokenCiphertext: c.TokenCiphertext, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func connectionFromDoc(d *connectionDoc) *repo.Connection {
	return &repo.Connection{
		ID: d.ID, UserID: d.UserID, Name: d.Name, BaseURL: d.BaseURL,
		TokenCiphertext: d.TokenCiphertext, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func policyToDoc(p *repo.Policy) policyDoc {
	return policyDoc{
		ID: p.ConnectionID + "/" + p.BaseID, ConnectionID: p.ConnectionID, BaseID: p.BaseID,
		UserID: p.UserID, Enabled: p.Enabled, Cron: p.Cron, Retention: p.Retention, UpdatedAt: p.UpdatedAt,
	}
}

func policyFromDoc(d *policyDoc) *repo.Policy {
	return &repo.Policy{
		ConnectionID: d.ConnectionID, BaseID: d.BaseID, UserID: d.UserID, Enabled: d.Enabled,
		Cron: d.Cron, Retention: d.Retention, UpdatedAt: d.UpdatedAt,
	}
}

func snapshotToDoc(s *repo.Snapshot) snapshotDoc {
	return snapshotDoc{
		ID: s.ID, UserID: s.UserID, ConnectionID: s.ConnectionID, BaseID: s.BaseID, BaseTitle: s.BaseTitle,
		Status: string(s.Status), Trigger: string(s.Trigger), Error: s.Error, Progress: s.Progress,
		ObjectKey: s.ObjectKey, SizeBytes: s.SizeBytes, RecordCount: s.RecordCount, LinkCount: s.LinkCount,
		Tables: s.Tables, CreatedAt: s.CreatedAt, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
	}
}

func snapshotFromDoc(d *snapshotDoc) *repo.Snapshot {
	return &repo.Snapshot{
		ID: d.ID, UserID: d.UserID, ConnectionID: d.ConnectionID, BaseID: d.BaseID, BaseTitle: d.BaseTitle,
		Status: repo.SnapshotStatus(d.Status), Trigger: repo.SnapshotTrigger(d.Trigger), Error: d.Error,
		Progress: d.Progress, ObjectKey: d.ObjectKey, SizeBytes: d.SizeBytes, RecordCount: d.RecordCount,
		LinkCount: d.LinkCount, Tables: d.Tables, CreatedAt: d.CreatedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
	}
}
