package grpcmesh

import (
	"context"
	"fmt"

	databasev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/database/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
)

// SidecarDatabase wraps a gRPC connection to a sidecar module's DatabaseService
// and implements contracts.DatabaseProvider by forwarding over gRPC.
type SidecarDatabase struct {
	client databasev1.DatabaseServiceClient
}

// NewSidecarDatabase creates a DatabaseProvider backed by a sidecar module's
// gRPC DatabaseService.
func NewSidecarDatabase(conn *grpc.ClientConn) *SidecarDatabase {
	return &SidecarDatabase{
		client: databasev1.NewDatabaseServiceClient(conn),
	}
}

func (s *SidecarDatabase) Open(ctx context.Context, params contracts.DatabaseParams) error {
	return nil
}

func (s *SidecarDatabase) Close(ctx context.Context) error {
	return nil
}

func (s *SidecarDatabase) Health(ctx context.Context) error {
	return nil
}

func protoToValue(v *databasev1.Value) any {
	if v == nil {
		return nil
	}
	switch vv := v.Kind.(type) {
	case *databasev1.Value_StringVal:
		return vv.StringVal
	case *databasev1.Value_IntVal:
		return vv.IntVal
	case *databasev1.Value_FloatVal:
		return vv.FloatVal
	case *databasev1.Value_BoolVal:
		return vv.BoolVal
	case *databasev1.Value_BytesVal:
		return vv.BytesVal
	case *databasev1.Value_NullVal:
		return nil
	default:
		return nil
	}
}

func valueToProto(v any) *databasev1.Value {
	switch val := v.(type) {
	case string:
		return &databasev1.Value{Kind: &databasev1.Value_StringVal{StringVal: val}}
	case int64:
		return &databasev1.Value{Kind: &databasev1.Value_IntVal{IntVal: val}}
	case int:
		return &databasev1.Value{Kind: &databasev1.Value_IntVal{IntVal: int64(val)}}
	case float64:
		return &databasev1.Value{Kind: &databasev1.Value_FloatVal{FloatVal: val}}
	case bool:
		return &databasev1.Value{Kind: &databasev1.Value_BoolVal{BoolVal: val}}
	case []byte:
		return &databasev1.Value{Kind: &databasev1.Value_BytesVal{BytesVal: val}}
	default:
		return &databasev1.Value{Kind: &databasev1.Value_NullVal{NullVal: true}}
	}
}

type dbRows struct {
	columns []string
	rows    []*databasev1.Row
	pos     int
}

func (r *dbRows) Next() bool {
	if r.pos >= len(r.rows) {
		return false
	}
	r.pos++
	return true
}

func (r *dbRows) Scan(dest ...any) error {
	if r.pos == 0 || r.pos > len(r.rows) {
		return fmt.Errorf("scan called before Next or past end")
	}
	row := r.rows[r.pos-1]
	for i, d := range dest {
		if i < len(row.GetValues()) {
			val := protoToValue(row.GetValues()[i])
			assignScan(d, val)
		}
	}
	return nil
}

func (r *dbRows) Close() error {
	return nil
}

func assignScan(dest any, val any) {
	switch d := dest.(type) {
	case *string:
		if s, ok := val.(string); ok {
			*d = s
		}
	case *int64:
		if n, ok := val.(int64); ok {
			*d = n
		}
	case *float64:
		if f, ok := val.(float64); ok {
			*d = f
		}
	case *bool:
		if b, ok := val.(bool); ok {
			*d = b
		}
	case *[]byte:
		if b, ok := val.([]byte); ok {
			*d = b
		}
	}
}

func (s *SidecarDatabase) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	req := &databasev1.ExecRequest{
		Query: query,
		Args:  make([]*databasev1.Value, len(args)),
	}
	for i, arg := range args {
		req.Args[i] = valueToProto(arg)
	}
	resp, err := s.client.Exec(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("sidecar database exec: %w", err)
	}
	return resp.GetRowsAffected(), nil
}

func (s *SidecarDatabase) Query(ctx context.Context, query string, args ...any) (contracts.Rows, error) {
	req := &databasev1.QueryRequest{
		Query: query,
		Args:  make([]*databasev1.Value, len(args)),
	}
	for i, arg := range args {
		req.Args[i] = valueToProto(arg)
	}
	resp, err := s.client.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("sidecar database query: %w", err)
	}
	return &dbRows{
		columns: resp.GetColumns(),
		rows:    resp.GetRows(),
	}, nil
}

type dbTx struct {
	client databasev1.DatabaseServiceClient
	ctx    context.Context
	stmts  []*databasev1.Statement
}

func (t *dbTx) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	req := &databasev1.Statement{
		Query: query,
		Args:  make([]*databasev1.Value, len(args)),
	}
	for i, arg := range args {
		req.Args[i] = valueToProto(arg)
	}
	t.stmts = append(t.stmts, req)
	return 0, nil
}

func (t *dbTx) Query(ctx context.Context, query string, args ...any) (contracts.Rows, error) {
	return nil, fmt.Errorf("queries within transactions not supported over gRPC")
}

func (s *SidecarDatabase) Transaction(ctx context.Context, fn func(tx contracts.Tx) error) error {
	tx := &dbTx{client: s.client, ctx: ctx}
	if err := fn(tx); err != nil {
		return err
	}

	stmts := tx.stmts
	if len(stmts) == 0 {
		return nil
	}

	req := &databasev1.TransactionRequest{
		Statements: stmts,
	}
	_, err := s.client.Transaction(ctx, req)
	if err != nil {
		return fmt.Errorf("sidecar database transaction: %w", err)
	}
	return nil
}

func (s *SidecarDatabase) Migrate(ctx context.Context, migrations []contracts.Migration) error {
	req := &databasev1.MigrateRequest{
		Migrations: make([]*databasev1.Migration, len(migrations)),
	}
	for i, m := range migrations {
		req.Migrations[i] = &databasev1.Migration{
			Version: int32(m.Version),
			Name:    m.Name,
			UpSql:   m.Up,
			DownSql: m.Down,
		}
	}
	_, err := s.client.Migrate(ctx, req)
	if err != nil {
		return fmt.Errorf("sidecar database migrate: %w", err)
	}
	return nil
}

func (s *SidecarDatabase) Rollback(ctx context.Context, targetVersion int) error {
	_, err := s.client.Rollback(ctx, &databasev1.RollbackRequest{
		TargetVersion: int32(targetVersion),
	})
	if err != nil {
		return fmt.Errorf("sidecar database rollback: %w", err)
	}
	return nil
}
