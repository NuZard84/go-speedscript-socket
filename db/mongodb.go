package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type TypingSentence struct {
	Story           string `bson:"story"`
	TotalCharacters int    `bson:"totalCharacters"`
	TotalWords      int    `bson:"totalWords"`
	Hash            string `bson:"hash"`
}

var client *mongo.Client

func Connect(uri string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	client, err = mongo.Connect(ctx, options.Client().ApplyURI(uri))
	return err
}

func GetRandomSentence(ctx context.Context) (*TypingSentence, error) {
	collection := client.Database("SpeedScript").Collection("typingsentences")

	pipeline := mongo.Pipeline{
		{{Key: "$sample", Value: bson.D{{Key: "size", Value: 1}}}},
	}

	cursor, err := collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sentence TypingSentence
	if cursor.Next(ctx) {
		if err := cursor.Decode(&sentence); err != nil {
			return nil, err
		}
		return &sentence, nil
	}
	return nil, mongo.ErrNoDocuments
}
