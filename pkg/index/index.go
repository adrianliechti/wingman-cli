package index

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/adrianliechti/go-hnsw"
)

type Index struct {
	db    *gorm.DB
	graph *hnsw.Graph[uint]
}

type Record struct {
	ID string

	Text   string
	Vector []float32

	Metadata map[string]string
}

type Page[T any] struct {
	Items []T

	Cursor string
}

type ListOptions struct {
	Limit  *int
	Cursor string
}

type RecordModel struct {
	gorm.Model

	Text   string
	Vector datatypes.JSONSlice[float32]

	Metadata datatypes.JSONMap
}

func New(path string) (*Index, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})

	if err != nil {
		return nil, err
	}

	graph := hnsw.NewGraph[uint]()

	if err := db.AutoMigrate(&RecordModel{}); err != nil {
		return nil, err
	}

	i := &Index{
		db:    db,
		graph: graph,
	}

	if err := i.indexEmbeddings(); err != nil {
		return nil, err
	}

	return i, nil
}

func (i *Index) indexEmbeddings() error {
	var models []RecordModel

	result := i.db.Model(&RecordModel{}).FindInBatches(&models, 100, func(tx *gorm.DB, batch int) error {
		for _, m := range models {
			if m.Vector == nil {
				continue
			}

			i.graph.Add(hnsw.MakeNode(m.ID, m.Vector))
		}

		return nil
	})

	return result.Error
}

func (i *Index) List(ctx context.Context, options *ListOptions) (*Page[Record], error) {
	if options == nil {
		options = new(ListOptions)
	}

	type cursor struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}

	var limit int
	var offset int

	if options.Limit != nil {
		limit = *options.Limit
	}

	if options.Cursor != "" {
		var cursor cursor

		data, err := base64.StdEncoding.DecodeString(options.Cursor)

		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(data, &cursor); err != nil {
			return nil, err
		}

		limit = cursor.Limit
		offset = cursor.Offset
	}

	if limit <= 0 {
		limit = 10
	}

	var models []RecordModel

	if result := i.db.Offset(offset).Limit(limit).Find(&models); result.Error != nil {
		return nil, result.Error
	}

	page := &Page[Record]{}

	for _, m := range models {
		metadata := map[string]string{}

		for k, v := range m.Metadata {
			metadata[k] = v.(string)
		}

		page.Items = append(page.Items, Record{
			ID: fmt.Sprintf("%d", m.ID),

			Text:     m.Text,
			Vector:   m.Vector,
			Metadata: metadata,
		})
	}

	c := cursor{
		Limit:  limit,
		Offset: offset + len(page.Items),
	}

	data, _ := json.Marshal(c)
	page.Cursor = base64.StdEncoding.EncodeToString(data)

	return page, nil
}

func (i *Index) Index(ctx context.Context, record ...Record) error {
	for _, r := range record {
		m := &RecordModel{
			Text: r.Text,
		}

		if len(r.Vector) > 0 {
			m.Vector = datatypes.NewJSONSlice(r.Vector)
		}

		if len(r.Metadata) > 0 {
			metadata := datatypes.JSONMap{}

			for k, v := range r.Metadata {
				metadata[k] = v
			}

			m.Metadata = metadata
		}

		result := i.db.Clauses(clause.OnConflict{
			UpdateAll: true,
		}).Create(&m)

		if result.Error != nil {
			return result.Error
		}

		if len(r.Vector) > 0 {
			i.graph.Add(hnsw.MakeNode(m.ID, r.Vector))
		}
	}

	return nil
}

func (i *Index) Search(ctx context.Context, vector []float32, topK int) ([]Record, error) {
	var ids []uint

	nodes := i.graph.Search(vector, topK)

	for _, n := range nodes {
		ids = append(ids, n.Key)
	}

	var models []RecordModel

	if result := i.db.Find(&models, ids); result.Error != nil {
		return nil, result.Error
	}

	var result []Record

	for _, m := range models {
		metadata := map[string]string{}

		for k, v := range m.Metadata {
			metadata[k] = v.(string)
		}

		result = append(result, Record{
			ID: fmt.Sprintf("%d", m.ID),

			Text:     m.Text,
			Metadata: metadata,
		})
	}

	return result, nil
}

func (i *Index) Delete(ctx context.Context, ids ...string) error {
	var identifiers []uint

	for _, id := range ids {
		val, err := strconv.ParseUint(id, 10, 32)

		if err != nil {
			continue
		}

		identifiers = append(identifiers, uint(val))
	}

	result := i.db.Unscoped().Delete(&RecordModel{}, identifiers)
	return result.Error
}
