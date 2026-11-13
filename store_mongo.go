package main

// store_mongo.go — MongoDB mein titles + files save karna.

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoStore struct {
	titles *mongo.Collection
	files  *mongo.Collection
}

func NewMongoStore(ctx context.Context, uri, dbName string) (*MongoStore, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(cctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := client.Ping(cctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	db := client.Database(dbName)
	return &MongoStore{titles: db.Collection("nx_titles"), files: db.Collection("nx_files")}, nil
}

// LoadAll: koi sort nahi (free MongoDB mein bade sort fail ho jaate hain) — sorting Go mein hoti hai.
func (m *MongoStore) LoadAll(ctx context.Context) ([]*TitleDoc, []*FileDoc, error) {
	var ts []*TitleDoc
	var fs []*FileDoc
	c1, err := m.titles.Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, err
	}
	if err := c1.All(ctx, &ts); err != nil {
		return nil, nil, err
	}
	c2, err := m.files.Find(ctx, bson.M{})
	if err != nil {
		return nil, nil, err
	}
	if err := c2.All(ctx, &fs); err != nil {
		return nil, nil, err
	}
	return ts, fs, nil
}

func (m *MongoStore) SaveTitle(ctx context.Context, t *TitleDoc) error {
	_, err := m.titles.ReplaceOne(ctx, bson.M{"_id": t.Key}, t, options.Replace().SetUpsert(true))
	return err
}

func (m *MongoStore) SaveFile(ctx context.Context, f *FileDoc) error {
	_, err := m.files.ReplaceOne(ctx, bson.M{"_id": f.ID}, f, options.Replace().SetUpsert(true))
	return err
}

func (m *MongoStore) DeleteFile(ctx context.Context, id int) error {
	_, err := m.files.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (m *MongoStore) DeleteTitle(ctx context.Context, key string) error {
	_, err := m.titles.DeleteOne(ctx, bson.M{"_id": key})
	return err
}

func (m *MongoStore) AddViews(ctx context.Context, key string, n int) error {
	_, err := m.titles.UpdateOne(ctx, bson.M{"_id": key}, bson.M{"$inc": bson.M{"views": n}})
	return err
}
