#!/usr/bin/env swift

import AppKit
import Foundation

struct CastEvent {
    let time: Double
    let output: String
}

struct TerminalScreen {
    let columns: Int
    let rows: Int
    private(set) var lines = [""]

    mutating func write(_ raw: String) {
        let clean = raw.replacingOccurrences(
            of: #"\u001B(?:\[[0-?]*[ -/]*[@-~]|\][^\u0007]*(?:\u0007|\u001B\\))"#,
            with: "",
            options: .regularExpression
        )
        for scalar in clean.unicodeScalars {
            switch scalar.value {
            case 10:
                newline()
            case 13:
                continue
            case 9:
                let spaces = 4 - (lines[lines.count - 1].count % 4)
                for _ in 0..<spaces { append(" ") }
            case 0..<32, 127:
                continue
            default:
                append(String(scalar))
            }
        }
    }

    private mutating func append(_ character: String) {
        if lines[lines.count - 1].count >= columns { newline() }
        lines[lines.count - 1].append(character)
    }

    private mutating func newline() {
        lines.append("")
        if lines.count > rows { lines.removeFirst(lines.count - rows) }
    }
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data("error: \(message)\n".utf8))
    exit(1)
}

func json(_ line: Substring) -> Any {
    do {
        return try JSONSerialization.jsonObject(with: Data(line.utf8))
    } catch {
        fail("invalid asciicast JSON: \(error)")
    }
}

func lineColor(_ line: String) -> NSColor {
    if line.hasPrefix("PASS:") { return NSColor(calibratedRed: 0.36, green: 0.86, blue: 0.55, alpha: 1) }
    if line.hasPrefix("===") { return NSColor(calibratedRed: 0.45, green: 0.67, blue: 1.0, alpha: 1) }
    if line == "codex" { return NSColor(calibratedRed: 0.48, green: 0.86, blue: 0.92, alpha: 1) }
    if line == "exec" { return NSColor(calibratedRed: 0.96, green: 0.76, blue: 0.38, alpha: 1) }
    if line.localizedCaseInsensitiveContains("permission denied") || line.hasPrefix("error:") {
        return NSColor(calibratedRed: 1.0, green: 0.48, blue: 0.48, alpha: 1)
    }
    return NSColor(calibratedWhite: 0.88, alpha: 1)
}

func render(screen: TerminalScreen, title: String, progress: Double, elapsed: Double, to path: String) {
    let width = 1280
    let height = 720
    guard let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: width,
        pixelsHigh: height,
        bitsPerSample: 8,
        samplesPerPixel: 4,
        hasAlpha: true,
        isPlanar: false,
        colorSpaceName: .deviceRGB,
        bytesPerRow: 0,
        bitsPerPixel: 0
    ) else { fail("could not allocate video frame") }

    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)

    NSColor(calibratedRed: 0.035, green: 0.047, blue: 0.075, alpha: 1).setFill()
    NSRect(x: 0, y: 0, width: width, height: height).fill()
    NSColor(calibratedRed: 0.065, green: 0.082, blue: 0.125, alpha: 1).setFill()
    NSBezierPath(roundedRect: NSRect(x: 22, y: 20, width: 1236, height: 680), xRadius: 14, yRadius: 14).fill()

    let headerFont = NSFont.systemFont(ofSize: 18, weight: .semibold)
    let metaFont = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
    let terminalFont = NSFont.monospacedSystemFont(ofSize: 14, weight: .regular)
    (title as NSString).draw(
        at: NSPoint(x: 46, y: 660),
        withAttributes: [.font: headerFont, .foregroundColor: NSColor(calibratedWhite: 0.94, alpha: 1)]
    )
    let stamp = String(format: "%02d:%02d", Int(elapsed) / 60, Int(elapsed) % 60)
    (stamp as NSString).draw(
        at: NSPoint(x: 1164, y: 663),
        withAttributes: [.font: metaFont, .foregroundColor: NSColor(calibratedWhite: 0.62, alpha: 1)]
    )

    NSColor(calibratedRed: 0.12, green: 0.15, blue: 0.22, alpha: 1).setFill()
    NSRect(x: 46, y: 642, width: 1188, height: 3).fill()
    NSColor(calibratedRed: 0.36, green: 0.55, blue: 0.96, alpha: 1).setFill()
    NSRect(x: 46, y: 642, width: 1188 * max(0, min(1, progress)), height: 3).fill()

    let visible = Array(screen.lines.suffix(screen.rows))
    let lineHeight: CGFloat = 17.0
    let firstBaseline: CGFloat = 618
    for (index, line) in visible.enumerated() {
        let y = firstBaseline - CGFloat(index) * lineHeight
        (line as NSString).draw(
            at: NSPoint(x: 46, y: y),
            withAttributes: [.font: terminalFont, .foregroundColor: lineColor(line)]
        )
    }

    NSGraphicsContext.restoreGraphicsState()
    guard let png = bitmap.representation(using: .png, properties: [:]) else { fail("could not encode PNG frame") }
    do { try png.write(to: URL(fileURLWithPath: path), options: .atomic) }
    catch { fail("write frame: \(error)") }
}

guard CommandLine.arguments.count == 3 else {
    fail("usage: cast-to-mp4.swift INPUT.cast OUTPUT.mp4")
}
let input = URL(fileURLWithPath: CommandLine.arguments[1]).standardizedFileURL.path
let output = URL(fileURLWithPath: CommandLine.arguments[2]).standardizedFileURL.path
guard FileManager.default.fileExists(atPath: input) else { fail("cast not found: \(input)") }

let contents: String
do { contents = try String(contentsOfFile: input, encoding: .utf8) }
catch { fail("read cast: \(error)") }
let records = contents.split(separator: "\n", omittingEmptySubsequences: true)
guard let first = records.first, let header = json(first) as? [String: Any] else { fail("missing asciicast header") }
let columns = header["width"] as? Int ?? 120
let rows = header["height"] as? Int ?? 36
let idleLimit = header["idle_time_limit"] as? Double ?? 2.0
let title = header["title"] as? String ?? "Terminal recording"

var events: [CastEvent] = []
for record in records.dropFirst() {
    guard let fields = json(record) as? [Any], fields.count >= 3,
          let time = fields[0] as? Double, let kind = fields[1] as? String,
          let data = fields[2] as? String, kind == "o" else { continue }
    events.append(CastEvent(time: time, output: data))
}
guard !events.isEmpty else { fail("cast has no output events") }

let temporary = FileManager.default.temporaryDirectory
    .appendingPathComponent("runeward-cast-\(UUID().uuidString)", isDirectory: true)
do { try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: true) }
catch { fail("create temporary frame directory: \(error)") }
defer { try? FileManager.default.removeItem(at: temporary) }

var effectiveTimes = [0.0]
for index in events.indices {
    let previous = index == 0 ? 0 : events[index - 1].time
    effectiveTimes.append(effectiveTimes.last! + min(max(events[index].time - previous, 0.04), idleLimit))
}
let totalDuration = effectiveTimes.last! + 3.0
var screen = TerminalScreen(columns: columns, rows: rows)
var concat = "ffconcat version 1.0\n"

func addFrame(index: Int, duration: Double, elapsed: Double) {
    let name = String(format: "frame-%04d.png", index)
    let path = temporary.appendingPathComponent(name).path
    render(screen: screen, title: title, progress: elapsed / totalDuration, elapsed: elapsed, to: path)
    concat += "file '\(path.replacingOccurrences(of: "'", with: "'\\''"))'\n"
    concat += String(format: "duration %.6f\n", max(duration, 0.04))
}

addFrame(index: 0, duration: effectiveTimes[1], elapsed: 0)
for index in events.indices {
    screen.write(events[index].output)
    let current = effectiveTimes[index + 1]
    let next = index + 1 < events.count ? effectiveTimes[index + 2] : totalDuration
    addFrame(index: index + 1, duration: next - current, elapsed: current)
}
let finalFrame = temporary.appendingPathComponent(String(format: "frame-%04d.png", events.count)).path
concat += "file '\(finalFrame.replacingOccurrences(of: "'", with: "'\\''"))'\n"
let concatPath = temporary.appendingPathComponent("frames.ffconcat").path
do { try concat.write(toFile: concatPath, atomically: true, encoding: .utf8) }
catch { fail("write ffconcat manifest: \(error)") }

try? FileManager.default.removeItem(atPath: output)
let ffmpeg = Process()
ffmpeg.executableURL = URL(fileURLWithPath: "/usr/bin/env")
ffmpeg.arguments = [
    "ffmpeg", "-hide_banner", "-loglevel", "warning", "-f", "concat", "-safe", "0",
    "-i", concatPath, "-r", "30", "-c:v", "libx264", "-preset", "medium", "-crf", "20",
    "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", output,
]
ffmpeg.standardOutput = FileHandle.standardOutput
ffmpeg.standardError = FileHandle.standardError
do { try ffmpeg.run(); ffmpeg.waitUntilExit() }
catch { fail("start ffmpeg: \(error)") }
guard ffmpeg.terminationStatus == 0 else { fail("ffmpeg exited with status \(ffmpeg.terminationStatus)") }
print("Video written to \(output)")
