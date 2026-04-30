package capabilities

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SetLicenseExpiry returns a handler that updates the licence expiry date
// for every organisation in this on-prem setup. A setup hosts one or more
// orgs in the local mongo, and they all share a single licence — so this
// capability fans the new expiry across all org documents.
//
// The mongo connection is resolved on every invocation via the supplied
// resolver, so the agent always picks up the live MONGODB_* values from
// the product .env (rather than a stale value persisted at install time).
//
// The field written matches the Auth service's Org schema:
//
//	licenceExpiry  (Date, top-level)
//
// Expected params:
//
//	expires_at    (string, required)  RFC3339 timestamp, e.g. 2026-12-31T00:00:00Z
//	collection    (string, optional)  defaults to "orgs"
//	database      (string, optional)  overrides the resolver's DB
func SetLicenseExpiry(resolveMongo func() (uri, db string)) Handler {
	return func(ctx context.Context, params map[string]any) (string, error) {
		expiresRaw, _ := params["expires_at"].(string)
		if expiresRaw == "" {
			return "", fmt.Errorf("expires_at is required")
		}

		expires, err := time.Parse(time.RFC3339, expiresRaw)
		if err != nil {
			return "", fmt.Errorf("invalid expires_at: %w", err)
		}

		mongoURI, defaultDB := "", ""
		if resolveMongo != nil {
			mongoURI, defaultDB = resolveMongo()
		}
		if mongoURI == "" {
			return "", fmt.Errorf("mongo connection is not configured (check MONGODB_HOST/PORT in the product .env)")
		}

		db := defaultDB
		if v, ok := params["database"].(string); ok && v != "" {
			db = v
		}
		if db == "" {
			return "", fmt.Errorf("mongo database is not configured")
		}

		coll := "orgs"
		if v, ok := params["collection"].(string); ok && v != "" {
			coll = v
		}

		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(mongoURI))
		if err != nil {
			return "", fmt.Errorf("mongo connect: %w", err)
		}
		defer func() {
			disconnectCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = client.Disconnect(disconnectCtx)
		}()

		update := bson.M{
			"$set": bson.M{
				"licenceExpiry": expires,
				"updatedAt":     time.Now().UTC(),
			},
		}

		updateCtx, cancel2 := context.WithTimeout(ctx, 30*time.Second)
		defer cancel2()

		// Fan the new expiry across every org in this setup.
		res, err := client.Database(db).Collection(coll).UpdateMany(updateCtx, bson.M{}, update)
		if err != nil {
			return "", fmt.Errorf("mongo update: %w", err)
		}
		if res.MatchedCount == 0 {
			return "", fmt.Errorf("no orgs found in %s.%s", db, coll)
		}

		return fmt.Sprintf("updated licenceExpiry to %s for all orgs in %s.%s (matched=%d modified=%d)",
			expires.Format(time.RFC3339), db, coll, res.MatchedCount, res.ModifiedCount), nil
	}
}
