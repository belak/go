//go:generate go run ./cmd/gendoc -template ./cmd/gendoc/doc.go -out doc.go

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/johnstarich/go/gopages/cmd"
	"github.com/johnstarich/go/gopages/internal/flags"
	"github.com/pkg/errors"
	"golang.org/x/mod/modfile"
)

const (
	ghPagesBranch = "gh-pages"
)

func main() {
	mainArgs(run, os.Getwd, os.Args[1:]...)
}

func mainArgs(
	runner func(string, flags.Args) error,
	getWD func() (string, error),
	osArgs ...string,
) {
	args, usageOutput, err := flags.Parse(osArgs...)
	switch err {
	case nil:
	case flag.ErrHelp:
		fmt.Print(usageOutput)
		return
	default:
		fmt.Print(usageOutput)
		cmd.Exit(2)
	}

	log.SetOutput(ioutil.Discard) // disable godoc's internal logging

	modulePath, err := getWD()
	if err != nil {
		panic(errors.Wrap(err, "Failed to get current directory"))
	}

	if err := runner(modulePath, args); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		cmd.Exit(1)
	}
}

func run(modulePath string, args flags.Args) error {
	goMod := filepath.Join(modulePath, "go.mod")
	if _, err := os.Stat(goMod); os.IsNotExist(err) {
		return errors.New("go.mod not found in the current directory")
	}

	buf, err := ioutil.ReadFile(goMod)
	if err != nil {
		return err
	}

	modulePackage := modfile.ModulePath(buf)
	if modulePackage == "" {
		return errors.Errorf("Unable to find module package name in go.mod file: %s", goMod)
	}

	fmt.Println("Generating godoc static pages for module...", modulePackage)

	if !args.GitHubPages {
		fs := osfs.New("")
		return generateDocs(modulePath, modulePackage, args, fs, fs)
	}

	repoRoot, remote, err := getCurrentPathAndRemote(modulePath)
	if err != nil {
		return err
	}

	absOutputPath, err := filepath.Abs(args.OutputPath)
	if err != nil {
		return errors.Wrap(err, "Failed to get absolute path of output directory")
	}
	args.OutputPath, err = filepath.Rel(repoRoot, absOutputPath)
	if err != nil {
		return errors.Wrap(err, "Output path must be inside repository for gh-pages integration")
	}

	src := osfs.New("")
	fs := memfs.New()

	repo, err := git.Clone(memory.NewStorage(), fs, &git.CloneOptions{
		URL:           remote,
		ReferenceName: plumbing.NewBranchReferenceName(ghPagesBranch),
		SingleBranch:  true,
	})
	if err != nil {
		return errors.Wrap(err, "Failed to clone in-memory copy of repo. Be sure the 'gh-pages' orphaned branch exists: https://help.github.com/en/github/working-with-github-pages/creating-a-github-pages-site-with-jekyll#creating-your-site")
	}

	if err := generateDocs(modulePath, modulePackage, args, src, fs); err != nil {
		return err
	}

	fmt.Println("Committing and pushing changes to gh-pages branch...")

	workTree, err := repo.Worktree()
	if err != nil {
		return err
	}

	if _, err := workTree.Add("."); err != nil {
		return errors.Wrap(err, "Failed to add output dir to git")
	}

	commitMessage := "Update GoPages"
	if args.SiteTitle != "" {
		commitMessage += ": " + args.SiteTitle
	}
	commit, err := workTree.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{Name: "GoPages", When: time.Now()},
	})
	if err != nil {
		return errors.Wrap(err, "Failed to commit gopages files")
	}

	pushOpts := &git.PushOptions{}
	if args.GitHubPagesUser != "" || args.GitHubPagesToken != "" {
		pushOpts.Auth = &gitHTTP.BasicAuth{Username: args.GitHubPagesUser, Password: args.GitHubPagesToken}
	}
	err = repo.Push(pushOpts)
	return errors.Wrapf(err, "Failed to push gopages commit %q", commit)
}

func getCurrentPathAndRemote(repoPath string) (string, string, error) {
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return "", "", errors.Wrapf(err, "Failed to open repo at %q", repoPath)
	}

	fs, err := repo.Worktree()
	if err != nil {
		return "", "", errors.Wrap(err, "Failed to set up work tree for repo")
	}
	repoRoot := fs.Filesystem.Root()

	remote, err := repo.Remote(git.DefaultRemoteName)
	remoteURL := remote.Config().URLs[0]
	return repoRoot, remoteURL, errors.Wrap(err, "Failed to get repo remote")
}
