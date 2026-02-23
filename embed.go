package downloadonce

import "embed"

//go:embed templates/*
var TemplateFS embed.FS

//go:embed static/*
var StaticFS embed.FS

//go:embed migrations/*
var MigrationFS embed.FS

//go:embed scripts/*
var ScriptFS embed.FS
