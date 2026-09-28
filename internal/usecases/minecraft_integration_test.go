package usecases

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/java"
	"game-server-platform/internal/minecraft"
	"game-server-platform/internal/storage"
)

// This test uses a new flat world bound to loopback. It never opens the existing
// world and only reuses an EULA acceptance already saved beside the supplied jar.
func TestMinecraftBackupIntegration(t *testing.T) {
	jar := os.Getenv("GSP_MINECRAFT_JAR")
	if jar == "" {
		t.Skip("set GSP_MINECRAFT_JAR to an installed 1.20.4 server.jar to run the disposable-world check")
	}
	jar, err := filepath.Abs(jar)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := (storage.ServerFiles{}).EULAAccepted(filepath.Dir(jar))
	if err != nil || !accepted {
		t.Fatalf("this test requires prior EULA acceptance beside the jar: %v", err)
	}
	root := t.TempDir()
	copyFixtureFile(t, jar, filepath.Join(root, "server.jar"))
	copyFixtureFile(t, filepath.Join(filepath.Dir(jar), "eula.txt"), filepath.Join(root, "eula.txt"))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	properties := fmt.Sprintf("server-ip=127.0.0.1\nserver-port=%d\nonline-mode=false\nlevel-name=fixture-world\nlevel-type=minecraft:flat\ngenerate-structures=false\nview-distance=2\nsimulation-distance=2\nspawn-protection=0\n", port)
	properties += "generator-settings={\"layers\":[{\"block\":\"minecraft:bedrock\",\"height\":1},{\"block\":\"minecraft:dirt\",\"height\":2},{\"block\":\"minecraft:grass_block\",\"height\":1}],\"biome\":\"minecraft:plains\"}\n"
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte(properties), 0644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "integration-console.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	t.Cleanup(func() {
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("Minecraft console:\n%s", data)
		}
	})
	runtime := java.New(bufio.NewReader(strings.NewReader("")), log, log)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	installation, err := runtime.Check(ctx, domain.MinimumJavaVersion)
	if err != nil {
		t.Fatal(err)
	}
	process, err := runtime.Launch(ctx, installation, root)
	if err != nil {
		t.Fatal(err)
	}
	// Always supervise the newest process, including a failed restart.
	defer func() {
		select {
		case <-process.Done():
		default:
			process.Send("stop")
		}
		if err := process.Wait(); err != nil {
			t.Errorf("fixture shutdown: %v", err)
		}
	}()
	session := Session{
		Process: runtime, Installation: installation, Directory: root, Version: "1.20.4",
		Backups: verifiedSnapshotStore{t: t}, Clock: RealClock{},
	}
	if err := waitReady(ctx, session, process); err != nil {
		t.Fatal(err)
	}
	statusBefore := minecraftStatus(t, port)
	if !strings.Contains(statusBefore, "1.20.4") {
		t.Fatalf("unexpected server version: %s", statusBefore)
	}
	spawnChanged := minecraft.Response(func(line string) bool {
		return minecraft.ContainsResponse(line, "Set the world spawn point to 7, 90, 11 [0.0]")
	})
	changeContext, cancelChange := context.WithTimeout(ctx, 15*time.Second)
	defer cancelChange()
	if err := process.SendAndWait(changeContext, "setworldspawn 7 90 11", spawnChanged, "fixture world change"); err != nil {
		t.Fatal(err)
	}
	result := CreateBackup(ctx, session, process, "integration  проверка 🌍")
	if result.Process != nil {
		process = result.Process
	}
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Path == "" {
		t.Fatal("snapshot was not published")
	}
	// level.dat is gzip-compressed NBT. This exact named int tag proves the
	// command's identifiable world change reached the copied level metadata.
	level, err := os.Open(filepath.Join(result.Path, "world", "level.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer level.Close()
	compressed, err := gzip.NewReader(level)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	tag := append([]byte{3, 0, 6}, []byte("SpawnX")...)
	tag = binary.BigEndian.AppendUint32(tag, 7)
	if !bytes.Contains(data, tag) {
		t.Fatal("snapshot does not contain the changed spawn location")
	}
	message, err := os.ReadFile(filepath.Join(result.Path, "backup_message.txt"))
	if err != nil || string(message) != "integration  проверка 🌍" {
		t.Fatalf("message = %q, %v", message, err)
	}
	statusAfter := minecraftStatus(t, port)
	if !strings.Contains(statusAfter, "1.20.4") {
		t.Fatalf("restarted server did not answer correctly: %s", statusAfter)
	}
	if err := process.WaitFor(ctx, minecraft.Response(minecraft.Ready), "restarted readiness"); err != nil {
		t.Fatal(err)
	}
	t.Log("World change saved, every file matched before restart, and both status connections succeeded.")
}

type verifiedSnapshotStore struct {
	t *testing.T
	storage.BackupStore
}

func (store verifiedSnapshotStore) Create(ctx context.Context, plan domain.BackupPlan, message string) error {
	if err := store.BackupStore.Create(ctx, plan, message); err != nil {
		return err
	}
	source, err := fixtureTree(plan.WorldDirectory)
	if err != nil {
		return err
	}
	copy, err := fixtureTree(filepath.Join(plan.Destination, "world"))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(source, copy) {
		return fmt.Errorf("snapshot file hashes or directories differ from stopped world")
	}
	store.t.Logf("Compared %d world entries before restart.", len(source))
	return nil
}

func fixtureTree(root string) (map[string]string, error) {
	entries := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			entries[relative] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	return entries, err
}

func copyFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

// A status handshake opens a new connection before and after restart. This
// checks protocol availability; it does not replace a real LAN player test.
func minecraftStatus(t *testing.T, port int) string {
	t.Helper()
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1"
	handshake := []byte{0}                        // Packet ID.
	handshake = append(handshake, varInt(765)...) // Minecraft 1.20.4.
	handshake = append(handshake, varInt(len(address))...)
	handshake = append(handshake, []byte(address)...)
	handshake = binary.BigEndian.AppendUint16(handshake, uint16(port))
	handshake = append(handshake, 1) // Status state.
	packet := append(varInt(len(handshake)), handshake...)
	packet = append(packet, 1, 0) // Status request.
	if _, err := connection.Write(packet); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	readInt := func() int {
		var value int
		for shift := 0; shift < 35; shift += 7 {
			part, err := reader.ReadByte()
			if err != nil {
				t.Fatal(err)
			}
			value |= int(part&0x7f) << shift
			if part&0x80 == 0 {
				return value
			}
		}
		t.Fatal("invalid status VarInt")
		return 0
	}
	packetLength := readInt()
	if packetLength <= 0 || packetLength > 1024*1024 {
		t.Fatalf("invalid status length %d", packetLength)
	}
	if id := readInt(); id != 0 {
		t.Fatalf("unexpected packet %d", id)
	}
	length := readInt()
	if length < 0 || length > packetLength {
		t.Fatalf("invalid JSON length %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func varInt(value int) []byte {
	var result []byte
	for value > 127 {
		result = append(result, byte(value&127)|128)
		value >>= 7
	}
	return append(result, byte(value))
}
