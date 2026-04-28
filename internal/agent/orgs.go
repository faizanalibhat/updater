package agent

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// orgLicence is the per-org licence snapshot the agent ships to admin in
// every heartbeat. The shape mirrors the Org model in Auth
// (Auth/src/database/models/org.model.js): _id, name, licenceExpiry.
type orgLicence struct {
	OrgID         string     `json:"org_id" bson:"_id"`
	Name          string     `json:"name"   bson:"name"`
	LicenceExpiry *time.Time `json:"licence_expiry,omitempty" bson:"licenceExpiry,omitempty"`
}

// orgLicenceDoc is the bson decoder shape (ObjectID -> hex string).
type orgLicenceDoc struct {
	ID            primitive.ObjectID `bson:"_id"`
	Name          string             `bson:"name"`
	LicenceExpiry *time.Time         `bson:"licenceExpiry,omitempty"`
}

// collectOrgLicences reads every org from the on-prem mongo and returns a
// minimal {id, name, licenceExpiry} snapshot for each. Best-effort: returns
// nil on any error so the heartbeat can still go through.
func collectOrgLicences(ctx context.Context, mongoURI, db string) []orgLicence {
	if mongoURI == "" || db == "" {
		return nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil
	}
	defer func() {
		dctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = client.Disconnect(dctx)
	}()

	queryCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()

	cur, err := client.Database(db).Collection("orgs").Find(
		queryCtx,
		bson.M{},
		options.Find().SetProjection(bson.M{"name": 1, "licenceExpiry": 1}),
	)
	if err != nil {
		return nil
	}
	defer cur.Close(queryCtx)

	var out []orgLicence
	for cur.Next(queryCtx) {
		var d orgLicenceDoc
		if err := cur.Decode(&d); err != nil {
			continue
		}
		out = append(out, orgLicence{
			OrgID:         d.ID.Hex(),
			Name:          d.Name,
			LicenceExpiry: d.LicenceExpiry,
		})
	}
	return out
}
