package main

import (
	"fmt"

	"github.com/echovisionlab/geul-asset-optimizer/internal/config"
	"github.com/echovisionlab/geul-asset-optimizer/internal/storage"
	"github.com/echovisionlab/geul-asset-optimizer/internal/workdir"
)

type workResources struct {
	workDirs *workdir.Manager
	storage  *storage.S3Client
	removed  int
}

func initializeWorkResources(deps dependencies, cfg *config.Config) (workResources, error) {
	workDirs, err := deps.newWorkDirs(cfg.TempDir)
	if err != nil {
		return workResources{}, fmt.Errorf("initialize work directory manager: %w", err)
	}
	removed, err := deps.cleanupStaleWorkDirs(workDirs)
	if err != nil {
		return workResources{}, fmt.Errorf("clean stale asset optimizer temp files: %w", err)
	}
	s3Client, err := deps.newStorage(cfg)
	if err != nil {
		return workResources{}, fmt.Errorf("initialize S3 client: %w", err)
	}
	return workResources{workDirs: workDirs, storage: s3Client, removed: removed}, nil
}
