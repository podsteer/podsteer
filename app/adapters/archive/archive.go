// Package archive is the local half of copying files to and from a
// container: it unpacks the tar stream a container sends into a directory
// the operator chose, and packs a path they chose into the stream a
// container receives.
//
// THIS IS THE ONLY CODE THAT WRITES WHERE A CONTAINER TOLD IT TO. The stream
// comes from an image somebody else built, running a workload somebody else
// wrote, and tar has been the vehicle for path-traversal attacks for as long
// as it has existed — an entry named `../../.ssh/authorized_keys`, a symlink
// to `/etc` followed by a file "inside" it, a setuid binary that lands with
// its bit intact. Every rule below exists for one of those, and every one
// has a test that tries it.
//
// The writes go through os.Root, which resolves every path relative to the
// chosen directory inside the kernel and refuses anything that would leave
// it — including through a symlink that was already there, or that an
// earlier entry in the same archive created. The checks on entry names are
// made first anyway, so a hostile archive is refused by name with an error
// that says why, rather than failing on whichever syscall happened to catch
// it.
package archive

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Local packs and unpacks against this machine's filesystem.
type Local struct{}

// Compile-time proof that Local satisfies the port.
var _ ports.ArchivePort = Local{}

// permBits are the only mode bits a transferred file keeps. Setuid, setgid
// and the sticky bit are dropped deliberately: an operator downloading a
// binary out of a container should not end up with a setuid-root program in
// their home directory because the image shipped one, and the same in
// reverse.
const permBits = 0o777

// Extract unpacks r into dest. See ports.ArchivePort.
func (Local) Extract(ctx context.Context, r io.Reader, dest string, limits domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error) {
	limits = limits.WithDefaults()

	info, err := os.Stat(dest)
	if err != nil {
		return domain.TransferSummary{}, fmt.Errorf("opening destination: %w", err)
	}
	if !info.IsDir() {
		return domain.TransferSummary{}, fmt.Errorf("opening destination: %s is not a directory", dest)
	}

	root, err := os.OpenRoot(dest)
	if err != nil {
		return domain.TransferSummary{}, fmt.Errorf("opening destination: %w", err)
	}
	defer func() { _ = root.Close() }()

	var summary domain.TransferSummary
	reader := tar.NewReader(r)

	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return summary, nil
		}
		if err != nil {
			return summary, fmt.Errorf("reading archive: %w", err)
		}

		// A global PAX header describes the archive, not a file; the tar
		// reader surfaces it and there is nothing to write for it.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}

		summary.Entries++
		if summary.Entries > limits.MaxEntries {
			return summary, fmt.Errorf("%w: more than %d entries", domain.ErrTransferTooLarge, limits.MaxEntries)
		}

		rel, err := safeRelativePath(header.Name)
		if err != nil {
			return summary, err
		}
		if rel == "" {
			// "./" — the archive's own root directory, which is dest itself.
			continue
		}

		mode := fs.FileMode(header.Mode) & permBits

		switch header.Typeflag {
		case tar.TypeDir:
			// The owner can always write into a directory they just
			// downloaded, whatever the container had it as: a 0555
			// directory would make every entry inside it fail to land,
			// and the operator would have to chmod something they have not
			// yet been able to look at.
			if err := root.MkdirAll(rel, mode|0o700); err != nil {
				return summary, fmt.Errorf("creating directory %s: %w", rel, err)
			}
			if err := root.Chmod(rel, mode|0o700); err != nil {
				return summary, fmt.Errorf("setting mode on %s: %w", rel, err)
			}

		case tar.TypeReg:
			// Directories are not guaranteed to precede their contents in
			// a stream, so the parent is made on demand.
			if parent := filepath.Dir(rel); parent != "." {
				if err := root.MkdirAll(parent, 0o755); err != nil {
					return summary, fmt.Errorf("creating directory %s: %w", parent, err)
				}
			}

			written, err := extractFile(root, rel, mode, reader, limits.MaxBytes-summary.Bytes, progress)
			summary.Bytes += written
			if err != nil {
				return summary, err
			}
			summary.Files++

		case tar.TypeSymlink:
			if err := safeLinkTarget(rel, header.Linkname); err != nil {
				return summary, err
			}
			// Replaced rather than failed: a re-download over an earlier
			// one should overwrite the link exactly as it overwrites a
			// file, and Symlink itself refuses an existing name.
			if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return summary, fmt.Errorf("replacing %s: %w", rel, err)
			}
			if err := root.Symlink(header.Linkname, rel); err != nil {
				return summary, fmt.Errorf("creating symlink %s: %w", rel, err)
			}

		case tar.TypeLink:
			// A hard link names another entry of the same archive, so its
			// target is checked as a name exactly like the entry itself.
			// os.Root then refuses to link anything outside dest even if a
			// name somehow slipped past; the check here is what makes the
			// refusal say why.
			target, err := safeRelativePath(header.Linkname)
			if err != nil {
				return summary, err
			}
			if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return summary, fmt.Errorf("replacing %s: %w", rel, err)
			}
			if err := root.Link(target, rel); err != nil {
				return summary, fmt.Errorf("creating hard link %s: %w", rel, err)
			}

		default:
			// Device nodes, FIFOs, sockets: nothing a downloaded tree needs
			// and everything an operator would not want created under
			// their home directory. Noted, never silently dropped.
			summary.Notes = append(summary.Notes,
				fmt.Sprintf("skipped %s: not a file, directory or link", header.Name))
		}
	}
}

// extractFile writes one regular file's content, bounded by remaining.
//
// The bound is enforced by reading one byte past it: a stream carries no
// total, so the only way to know a file crosses the cap is to see it do so.
// Whatever was written before the cap stays on disk — a truncated file is
// visibly wrong in a way a silently absent one is not.
func extractFile(root *os.Root, rel string, mode fs.FileMode, content io.Reader, remaining int64, progress func(int64)) (int64, error) {
	if remaining < 0 {
		remaining = 0
	}

	file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode|0o600)
	if err != nil {
		return 0, fmt.Errorf("creating %s: %w", rel, err)
	}

	written, err := io.Copy(&countingWriter{w: file, progress: progress}, io.LimitReader(content, remaining+1))
	closeErr := file.Close()

	switch {
	case err != nil:
		return written, fmt.Errorf("writing %s: %w", rel, err)
	case written > remaining:
		return written, fmt.Errorf("%w: more than %d bytes", domain.ErrTransferTooLarge, remaining)
	case closeErr != nil:
		return written, fmt.Errorf("writing %s: %w", rel, closeErr)
	}

	// After the write, so the umask applied at creation does not decide the
	// result — and always with the owner able to read what they asked for.
	if err := root.Chmod(rel, mode|0o600); err != nil {
		return written, fmt.Errorf("setting mode on %s: %w", rel, err)
	}
	return written, nil
}

// Pack writes source to w as a tar stream. See ports.ArchivePort.
func (Local) Pack(ctx context.Context, w io.Writer, source string, limits domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error) {
	limits = limits.WithDefaults()

	// The path the operator picked in a dialog may itself be a symlink — a
	// Desktop alias, a synced folder — and they meant what it points at.
	// That is the one link this function ever follows: everything beneath
	// it is read with Lstat, so a link inside the tree stays a link.
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return domain.TransferSummary{}, fmt.Errorf("reading %s: %w", source, err)
	}
	base := filepath.Base(resolved)
	if base == string(filepath.Separator) || base == "." {
		return domain.TransferSummary{}, fmt.Errorf("reading %s: a filesystem root cannot be uploaded", source)
	}

	var summary domain.TransferSummary
	writer := tar.NewWriter(w)

	walk := func(local string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("reading %s: %w", local, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, err := filepath.Rel(resolved, local)
		if err != nil {
			return fmt.Errorf("reading %s: %w", local, err)
		}
		name := base
		if rel != "." {
			name = path.Join(base, filepath.ToSlash(rel))
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", local, err)
		}

		switch {
		case info.Mode().IsDir():
			summary.Entries++
			return writeHeader(writer, &tar.Header{
				Typeflag: tar.TypeDir,
				Name:     name + "/",
				Mode:     int64(info.Mode() & permBits),
				ModTime:  info.ModTime(),
			}, limits, summary.Entries)

		case info.Mode().IsRegular():
			summary.Entries++
			if err := writeHeader(writer, &tar.Header{
				Typeflag: tar.TypeReg,
				Name:     name,
				Mode:     int64(info.Mode() & permBits),
				Size:     info.Size(),
				ModTime:  info.ModTime(),
			}, limits, summary.Entries); err != nil {
				return err
			}
			if summary.Bytes+info.Size() > limits.MaxBytes {
				return fmt.Errorf("%w: more than %d bytes", domain.ErrTransferTooLarge, limits.MaxBytes)
			}
			written, err := packFile(writer, local, info.Size(), progress)
			summary.Bytes += written
			if err != nil {
				return err
			}
			summary.Files++
			return nil

		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(local)
			if err != nil {
				return fmt.Errorf("reading %s: %w", local, err)
			}
			if !linkStaysInside(resolved, local, target) {
				// Left out rather than followed: following it would upload
				// whatever it points at — a file outside the selection the
				// operator never chose — and archiving it as a link would
				// plant a pointer into the container's filesystem that
				// means nothing there.
				summary.Notes = append(summary.Notes,
					fmt.Sprintf("skipped %s: symlink to %s, outside the selection", name, target))
				return nil
			}
			summary.Entries++
			return writeHeader(writer, &tar.Header{
				Typeflag: tar.TypeSymlink,
				Name:     name,
				Linkname: target,
				Mode:     int64(info.Mode() & permBits),
				ModTime:  info.ModTime(),
			}, limits, summary.Entries)

		default:
			summary.Notes = append(summary.Notes,
				fmt.Sprintf("skipped %s: not a file, directory or link", name))
			return nil
		}
	}

	if err := filepath.WalkDir(resolved, walk); err != nil {
		return summary, err
	}

	// Close writes the end-of-archive marker; without it tar on the far end
	// reports a truncated archive even when every entry arrived.
	if err := writer.Close(); err != nil {
		return summary, fmt.Errorf("finishing archive: %w", err)
	}
	return summary, nil
}

// writeHeader emits one entry's header, enforcing the entry cap first.
//
// Uid, Gid, Uname and Gname are left at their zero values on purpose. The
// local account's name and numeric id describe this machine, not the file,
// and they would otherwise be written into the container — where tar
// running as root would apply the id to files that then belong to a user
// the container has never heard of.
func writeHeader(writer *tar.Writer, header *tar.Header, limits domain.TransferLimits, entries int) error {
	if entries > limits.MaxEntries {
		return fmt.Errorf("%w: more than %d entries", domain.ErrTransferTooLarge, limits.MaxEntries)
	}
	header.Format = tar.FormatPAX
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("writing archive entry %s: %w", header.Name, err)
	}
	return nil
}

// packFile copies exactly size bytes of one file into the archive.
//
// Exactly, because the header has already promised that many: a file that
// grew or shrank between Lstat and here would corrupt every entry after it,
// so a short read is an error rather than a shorter file.
func packFile(writer *tar.Writer, local string, size int64, progress func(int64)) (int64, error) {
	file, err := os.Open(local)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", local, err)
	}
	defer func() { _ = file.Close() }()

	written, err := io.CopyN(&countingWriter{w: writer, progress: progress}, file, size)
	if err != nil {
		return written, fmt.Errorf("reading %s: %w", local, err)
	}
	return written, nil
}

// safeRelativePath turns an archive entry's name into a path relative to
// the destination, or refuses it.
//
// Refused: an absolute name, any `..` component, and a backslash anywhere.
// The last is stricter than Linux needs — a backslash is a legal filename
// character there — but on Windows it is a separator, and an entry named
// `..\..\x` would be a traversal that the forward-slash check never sees.
// One rule for every platform is worth the rare file it refuses.
//
// Returns "" for an entry that names the destination itself ("./"), which
// has nothing to create.
func safeRelativePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: an entry with no name", domain.ErrUnsafeArchiveEntry)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: %q is absolute", domain.ErrUnsafeArchiveEntry, name)
	}
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("%w: %q contains a backslash", domain.ErrUnsafeArchiveEntry, name)
	}

	var parts []string
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("%w: %q climbs out with \"..\"", domain.ErrUnsafeArchiveEntry, name)
		}
		parts = append(parts, part)
	}
	return filepath.Join(parts...), nil
}

// safeLinkTarget refuses a symlink whose target could leave the destination.
//
// The target is judged from where the link sits: `../etc` inside `a/b/` is
// `a/etc`, which is fine, while the same target at the top level is not.
// An absolute target is refused outright, whatever it names — even one that
// happens to point inside dest today would point somewhere else if the tree
// were moved.
func safeLinkTarget(rel, target string) error {
	if target == "" {
		return fmt.Errorf("%w: symlink %q has no target", domain.ErrUnsafeArchiveEntry, rel)
	}
	if path.IsAbs(target) || strings.HasPrefix(target, `\`) || strings.Contains(target, `\`) {
		return fmt.Errorf("%w: symlink %q points at %q", domain.ErrUnsafeArchiveEntry, rel, target)
	}

	resolved := path.Clean(path.Join(path.Dir(filepath.ToSlash(rel)), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symlink %q points at %q, outside the chosen directory", domain.ErrUnsafeArchiveEntry, rel, target)
	}
	return nil
}

// linkStaysInside reports whether a local symlink at link, with the given
// target, resolves to somewhere under root — the selection being uploaded.
func linkStaysInside(root, link, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(link), target))
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// countingWriter reports each write's size to progress, when there is one.
type countingWriter struct {
	w        io.Writer
	progress func(int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 && c.progress != nil {
		c.progress(int64(n))
	}
	return n, err
}
