package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

func prepareCollectionInput(commandName, inputDir, archivePath, identityPath string) (string, func(), error) {
	inputDir = strings.TrimSpace(inputDir)
	archivePath = strings.TrimSpace(archivePath)
	identityPath = strings.TrimSpace(identityPath)
	if archivePath == "" {
		return inputDir, func() {}, nil
	}

	identity, err := loadArchivePrivateKey(identityPath)
	if err != nil {
		return "", nil, err
	}
	tempDir, err := os.MkdirTemp("", "retriever-"+commandName+"-archive-*")
	if err != nil {
		return "", nil, fmt.Errorf("create %s archive temp directory: %w", commandName, err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	slog.Info("retriever "+commandName+" archive unpacking started",
		slog.String("archive", archivePath),
	)
	if err := unpackEncryptedCollectionArchiveToDirectory(file, tempDir, identity); err != nil {
		cleanup()
		return "", nil, err
	}
	slog.Info("retriever "+commandName+" archive unpacking completed",
		slog.String("archive", archivePath),
	)
	return tempDir, cleanup, nil
}
