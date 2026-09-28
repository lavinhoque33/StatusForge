package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/buildinfo"
	"github.com/lavinhoque33/statusforge/backend/internal/config"
	"github.com/lavinhoque33/statusforge/backend/internal/dataexport"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type usageError string

func (e usageError) Error() string { return string(e) }
func command(args []string) error {
	if len(args) == 0 {
		return run()
	}
	switch args[0] {
	case "serve":
		if len(args) != 1 {
			return usageError("serve accepts no arguments")
		}
		return run()
	case "version":
		if len(args) != 1 {
			return usageError("version accepts no arguments")
		}
		fmt.Println(buildinfo.Current())
		return nil
	case "export", "import":
		return transfer(args[0], args[1:])
	default:
		return usageError(
			"usage: statusforge [serve|version|export [--table NAME] [--out PATH]|import --in PATH --table NAME]",
		)
	}
}

func transfer(kind string, args []string) error {
	fs := flag.NewFlagSet(kind, flag.ContinueOnError)
	table := fs.String("table", "", "DynamoDB Local table")
	out := fs.String("out", "", "private export path")
	in := fs.String("in", "", "export file to import")
	if e := fs.Parse(args); e != nil {
		return usageError(e.Error())
	}
	if len(fs.Args()) != 0 {
		return usageError("unexpected arguments")
	}
	if kind == "import" && (*table == "" || *in == "" || *out != "") {
		return usageError("import requires --in and --table")
	}
	if kind == "export" && *in != "" {
		return usageError("export does not accept --in")
	}
	cfg, e := config.Load(os.LookupEnv)
	if e != nil {
		return e
	}
	if *table == "" {
		*table = cfg.DynamoDBTable
	}
	if e = config.ValidateTable(*table); e != nil {
		return e
	}
	u, _ := url.Parse(cfg.DynamoDBEndpoint)
	client := localdynamo.New(
		cfg.DynamoDBEndpoint,
		u.Hostname(),
		cfg.DynamoDBRegion,
		cfg.DynamoDBAccessKeyID,
		cfg.DynamoDBSecretAccessKey,
	)
	db := client.DynamoDB()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if kind == "export" {
		if *out == "" {
			*out = filepath.Join(
				".local",
				"exports",
				fmt.Sprintf("%s-%s.jsonl", *table, time.Now().UTC().Format("20060102T150405Z")),
			)
		}
		result, e := dataexport.Export(ctx, db, *table, *out, buildinfo.Current())
		if e != nil {
			return e
		}
		if result.Warning {
			fmt.Fprintln(
				os.Stderr,
				"warning: API liveness item is present; export may not be a consistent snapshot. Stop the API first",
			)
		}
		fmt.Printf(
			"export: %d items sha256=%s duration=%s path=%s\n",
			result.Items,
			result.Digest,
			result.Duration,
			*out,
		)
		return nil
	}
	persistence := store.New(client, *table, cfg.ReadinessTimeout, time.Now)
	result, e := dataexport.Import(ctx, db, *table, *in, persistence.Initialize)
	if e != nil {
		return e
	}
	fmt.Printf(
		"import: %d items sha256=%s duration=%s table=%s\n",
		result.Items,
		result.Digest,
		result.Duration,
		*table,
	)
	return nil
}
