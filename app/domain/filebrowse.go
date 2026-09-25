// Listing one directory inside a container.
//
// THE OTHER HALF OF FILE COPY. Copying already works — tar runs inside the
// container over an exec session, exactly as kubectl cp does — and it requires
// the operator to know the path already. This is the half that lets them look.
//
// WHY THIS EMITS ITS OWN FORMAT RATHER THAN PARSING SOMEBODY ELSE'S. Three
// mechanisms were considered and all three were rejected, which is worth
// writing down because each is the obvious answer:
//
//   - `ls -l` HAS NO PINNABLE FORMAT. Busybox and GNU disagree on columns and
//     on the date field, GNU escapes a space in a name only with a flag
//     busybox does not have, and a name containing a NEWLINE splits into two
//     rows that both look like valid entries. Worse, nothing tells you which
//     `ls` you got until after you have parsed it. This is the classic source
//     of silent wrongness in every tool that has tried it.
//   - `find -printf` WOULD BE PINNABLE and is a GNU extension busybox does not
//     build. The fallback, `find -print0`, gives names with no type and no
//     size — so the shape of the result would depend on the image, which is
//     worse than either branch alone.
//   - TAR HEADERS ARE CORRECT AND TOO EXPENSIVE. `tar cf -` writes a binary
//     format whose names are length-delimited byte strings, so spaces,
//     newlines and non-UTF-8 all survive with no parser to get wrong — but it
//     RECURSES AND STREAMS FILE BODIES, and skipping a body over a network
//     stream still reads it. Listing a directory of large files would pull
//     gigabytes over the wire to learn a dozen names. `--no-recursion` is
//     GNU-only.
//
// So the script below emits a format PodSteer designed, from primitives that
// are guaranteed: the shell's own `test`, and `printf`. It is the same
// arrangement ProbeCommand and ParseProbeOutput already use, for the same
// reason — the command and the parser are one protocol, they live together,
// and a test asserts that what one emits is what the other reads.
//
// WHAT IS NOT GUARANTEED IS ABSENT RATHER THAN GUESSED. `stat` is the one
// tool whose output is read, its format pinned on the command line rather
// than inferred (`-c '%s %Y'` is two integers in both coreutils and busybox,
// with no locale and no column alignment involved). An image without it gets
// a listing with no sizes and no dates — and the interface says so, rather
// than showing a nought that reads as an empty file.
package domain

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ListingEntryCap is how many entries one listing carries.
//
// A LIMIT ON WHAT A PERSON READS, not on what a machine can hold — which is
// why it is nothing like DefaultTransferMaxEntries, whose 100,000 bounds a
// transfer. Two thousand rows is already past the point of reading; what
// matters is that going past it is SAID rather than silently cut.
const ListingEntryCap = 2000

// listingUnsupportedMarker is the line the script prints when the container
// has no shell to run it with. It mirrors probeUnsupportedMarker, and it is
// the same distinction: a fact about the image, not a failure of the read.
const listingUnsupportedMarker = "unsupported"

var (
	// ErrListingUnreadable means the container printed something that is not
	// a listing. NOTHING IS ASSUMED FROM IT: half a directory read as a whole
	// one would have somebody looking for a file in the wrong place.
	ErrListingUnreadable = errors.New("the listing produced no readable result")

	// ErrInvalidListPath means the path could not be used.
	ErrInvalidListPath = errors.New("invalid container path")
)

// EntryKind is what one entry turned out to be.
type EntryKind string

const (
	EntryFile    EntryKind = "file"
	EntryDir     EntryKind = "dir"
	EntrySymlink EntryKind = "symlink"
	EntryOther   EntryKind = "other"
)

// FileEntry is one line of a directory.
type FileEntry struct {
	Name string
	Kind EntryKind

	// Size and SizeKnown are separate for the reason MetricsStatus and
	// ImageReport's size are: an image without `stat` has no size to report,
	// and a nought there reads as an empty file.
	Size      int64
	SizeKnown bool

	// ModifiedUnix and TimeKnown, likewise.
	ModifiedUnix int64
	TimeKnown    bool

	// LinkTarget is where a symlink points, when readlink was available.
	LinkTarget string
	// ResolvesToDir reports that following the symlink reaches a directory,
	// which is what lets the interface offer to follow it. FOLLOWING IS THE
	// OPERATOR'S CLICK, never this listing's: a link can leave the tree they
	// think they are in.
	ResolvesToDir bool

	// NameReadable is false when the name is not valid UTF-8.
	//
	// SUCH A NAME CANNOT SURVIVE THE ROUND TRIP. Go's JSON encoder replaces
	// invalid UTF-8 with U+FFFD silently, so a name that looked right would
	// come back wrong and download a different file. The entry is listed —
	// hiding it would be a lie about the directory — and marked, and the
	// interface refuses to act on it.
	NameReadable bool
}

// DirectoryListing is one directory as the container reported it.
type DirectoryListing struct {
	Path    string
	Entries []FileEntry

	// Truncated and Cap say the listing stopped rather than ended.
	Truncated bool
	Cap       int

	// SizeSource names the tool sizes came from, or "" when none did.
	SizeSource string

	// Notes name what was NOT listed and why — the TransferSummary.Notes
	// rule: an entry dropped silently is a directory misrepresented.
	Notes []string
}

// CleanListPath vets a path before any exec is opened.
//
// THE ROOT IS ALLOWED HERE AND REFUSED BY SplitRemotePath, which is a decision
// rather than an inconsistency: `tar cf - -C / /` archives an entire container
// and is never what somebody meant, while listing `/` is one directory, costs
// nothing, and is the first place anybody looks.
func CleanListPath(remote string) (string, error) {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return "", fmt.Errorf("%w: no path", ErrInvalidListPath)
	}
	// A NUL byte truncates the path wherever it is read, so a name carrying
	// one could name a different file on the far side than it does here.
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("%w: a path may not contain a NUL byte", ErrInvalidListPath)
	}
	if !strings.HasPrefix(trimmed, "/") {
		// Resolved against the container's working directory by the caller
		// before it gets here, so the backend never has to guess what "."
		// meant in an image it cannot see.
		return "", fmt.Errorf("%w: a container path must be absolute", ErrInvalidListPath)
	}

	return path.Clean(trimmed), nil
}

// ListCommand is the command that lists one directory.
//
// A SHELL, DELIBERATELY, where the file copy uses a fixed argv. Listing needs
// globbing and `test`, which are the shell's and nothing else's — the same
// judgement ProbeCommand makes, and with the same protection: the path is
// single-quoted by shellQuote and bound to a variable once, so nothing a
// directory can be called reaches the shell as syntax.
func ListCommand(dir string) []string {
	return []string{"/bin/sh", "-c", listScript(dir)}
}

// listScript is the POSIX script that produces the listing.
//
// POSIX ONLY — no arrays, no [[ ]], no `local`, no `-printf` — because the
// image on the far end is as likely to be busybox as Debian. Every tool
// beyond the shell itself is tested with `command -v` before use and its
// absence changes what is reported rather than failing the listing.
func listScript(dir string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "D=%s\n", shellQuote(dir))
	fmt.Fprintf(&b, "CAP=%d\n", ListingEntryCap)

	// A LITERAL NEWLINE INSIDE SINGLE QUOTES, and it has to be written this
	// way. `NL=$(printf '\n')` is the obvious form and is silently wrong:
	// command substitution strips trailing newlines, so it yields the EMPTY
	// string, and the pattern `*"$NL"*` below then matches EVERY name there
	// is. Written that way this script refused an entire directory and
	// reported it as a directory full of unlistable names — which is what it
	// did, until a test ran it against a real one.
	b.WriteString("NL='\n'\n")

	// The shell must be able to reach the directory at all. `cd` rather than
	// a test, because it is the same permission the loop below needs and it
	// fails with the shell's own words.
	b.WriteString("cd -- \"$D\" 2>/dev/null || { printf 'denied\\n'; exit 0; }\n")

	// `stat` is the ONE tool whose output is parsed, and its format is pinned
	// on the command line rather than inferred.
	// THE EXACT INVOCATION IS TESTED, not the tool's presence. A `stat` on
	// PATH is not a `stat` that takes `-c` — BSD's takes `-f` — and asking
	// `command -v` would have the listing claim sizes it then could not
	// produce. One probe of the real thing settles it.
	b.WriteString("S=0\n")
	b.WriteString("stat -c '%s %Y' -- . >/dev/null 2>&1 && S=1\n")
	b.WriteString("R=0\n")
	b.WriteString("command -v readlink >/dev/null 2>&1 && R=1\n")
	b.WriteString("printf 'tools %s %s\\n' \"$S\" \"$R\"\n")

	// THREE GLOBS, because POSIX sh has no nullglob and no dotglob: the first
	// takes the ordinary entries, the second the dotfiles, the third the ones
	// beginning with two dots that are not `..` itself. An unmatched glob
	// arrives literally, which the existence test below discards.
	b.WriteString("N=0\n")
	b.WriteString("for f in ./* ./.[!.]* ./..?*; do\n")
	// `-e` follows a symlink and fails on a broken one, so `-L` beside it is
	// what keeps a DANGLING SYMLINK in the listing rather than dropping it.
	b.WriteString("  [ -e \"$f\" ] || [ -L \"$f\" ] || continue\n")
	b.WriteString("  n=${f#./}\n")
	b.WriteString("  case \"$n\" in *\"$NL\"*) printf 'skipped newline\\n'; continue;; esac\n")

	b.WriteString("  N=$((N+1))\n")
	b.WriteString("  if [ \"$N\" -gt \"$CAP\" ]; then printf 'truncated\\n'; break; fi\n")

	// -L BEFORE -d, and this ordering is the whole correctness of the kind
	// column: `test -d` FOLLOWS a symlink, so a link to a directory would
	// otherwise be reported as a directory and navigated into as one.
	b.WriteString("  if [ -L \"$f\" ]; then k=symlink\n")
	b.WriteString("  elif [ -d \"$f\" ]; then k=dir\n")
	b.WriteString("  elif [ -f \"$f\" ]; then k=file\n")
	b.WriteString("  else k=other; fi\n")

	b.WriteString("  sz=-; mt=-\n")
	b.WriteString("  if [ \"$S\" = 1 ]; then\n")
	b.WriteString("    out=$(stat -c '%s %Y' -- \"$f\" 2>/dev/null) && { sz=${out%% *}; mt=${out##* }; }\n")
	b.WriteString("  fi\n")

	b.WriteString("  lt=-; rd=0\n")
	b.WriteString("  if [ \"$k\" = symlink ]; then\n")
	b.WriteString("    [ -d \"$f\" ] && rd=1\n")
	b.WriteString("    if [ \"$R\" = 1 ]; then lt=$(readlink -- \"$f\" 2>/dev/null) || lt=-; fi\n")
	b.WriteString("  fi\n")

	// THE NAME IS LAST AND PRINTED WITH printf, NEVER echo. dash's echo
	// interprets backslash escapes, so a file genuinely called `a\tb` would be
	// renamed by the act of listing it. Last, so a name containing spaces
	// needs no quoting: everything after the fifth field is the name.
	b.WriteString("  printf 'e %s %s %s %s %s\\n' \"$k\" \"$sz\" \"$mt\" \"$lt\" \"$rd\"\n")
	b.WriteString("  printf 'n %s\\n' \"$n\"\n")
	b.WriteString("done\n")
	b.WriteString("printf 'end %s\\n' \"$N\"\n")

	return b.String()
}

// ParseListOutput reads what the script printed.
//
// The same triple ParseProbeOutput returns, so the adapter branches the same
// way: `ok` false with the unsupported marker is the container saying it has
// nothing, and `ok` false with ErrListingUnreadable is this build unable to
// read what it got. NEVER A PARTIAL LISTING PRESENTED AS A WHOLE ONE.
func ParseListOutput(raw, dir string) (DirectoryListing, bool, error) {
	listing := DirectoryListing{Path: dir, Cap: ListingEntryCap}

	var (
		pending  *FileEntry
		newlines int
		ended    bool
	)

	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}

		switch {
		case line == listingUnsupportedMarker:
			return DirectoryListing{}, false, errors.New("this container has no shell to list a directory with")

		case line == "denied":
			return DirectoryListing{}, false, errors.New("the container's own user cannot read that directory")

		case strings.HasPrefix(line, "tools "):
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == "1" {
				listing.SizeSource = "stat"
			}

		case line == "skipped newline":
			newlines++

		case line == "truncated":
			listing.Truncated = true

		case strings.HasPrefix(line, "end "):
			ended = true

		case strings.HasPrefix(line, "e "):
			entry, ok := parseEntryLine(line)
			if !ok {
				return DirectoryListing{}, false, ErrListingUnreadable
			}
			pending = &entry

		case strings.HasPrefix(line, "n "):
			if pending == nil {
				return DirectoryListing{}, false, ErrListingUnreadable
			}
			name := line[len("n "):]
			pending.Name = name
			pending.NameReadable = utf8.ValidString(name)

			// The cap is enforced HERE as well as in the script, because a
			// container that printed more than it was told to must not be
			// able to enlarge a listing by saying so.
			if len(listing.Entries) >= ListingEntryCap {
				listing.Truncated = true
				pending = nil
				continue
			}
			listing.Entries = append(listing.Entries, *pending)
			pending = nil

		default:
			// Unrecognised lines are IGNORED rather than refused, the
			// forgiveness ParseProbeOutput gives for the same reason: a
			// future field must not break an older build.
		}
	}

	if !ended && len(listing.Entries) == 0 && !listing.Truncated {
		return DirectoryListing{}, false, ErrListingUnreadable
	}

	if newlines > 0 {
		listing.Notes = append(listing.Notes, fmt.Sprintf(
			"%s not listed: the name contains a newline, which no line-based listing can carry unambiguously",
			plural(newlines, "entry was", "entries were")))
	}
	if listing.SizeSource == "" {
		listing.Notes = append(listing.Notes,
			"sizes and dates are not shown: this image has no stat")
	}

	SortListing(listing.Entries)
	return listing, true, nil
}

// parseEntryLine reads the fixed five fields that precede a name.
func parseEntryLine(line string) (FileEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) != 6 {
		return FileEntry{}, false
	}

	entry := FileEntry{Kind: EntryKind(fields[1])}
	switch entry.Kind {
	case EntryFile, EntryDir, EntrySymlink, EntryOther:
	default:
		return FileEntry{}, false
	}

	if size, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
		entry.Size, entry.SizeKnown = size, true
	}
	if modified, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
		entry.ModifiedUnix, entry.TimeKnown = modified, true
	}
	if fields[4] != "-" {
		entry.LinkTarget = fields[4]
	}
	entry.ResolvesToDir = fields[5] == "1"

	return entry, true
}

// SortListing puts directories first, then names.
//
// IN THE DOMAIN so the Go side and the interface cannot disagree about the
// order — the same reason stripModel builds the fleet strip here rather than
// in a component.
func SortListing(entries []FileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		leftDir := left.Kind == EntryDir || (left.Kind == EntrySymlink && left.ResolvesToDir)
		rightDir := right.Kind == EntryDir || (right.Kind == EntrySymlink && right.ResolvesToDir)
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}
