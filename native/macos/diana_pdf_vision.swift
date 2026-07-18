import AppKit
import Foundation
import PDFKit
import Vision

private struct PageOutput: Codable {
    let number: Int
    let source: String
    let text: String
}

private struct DocumentOutput: Codable {
    let totalPages: Int
    let processedPages: Int
    let textPages: Int
    let visionPages: Int
    let pages: [PageOutput]

    enum CodingKeys: String, CodingKey {
        case totalPages = "total_pages"
        case processedPages = "processed_pages"
        case textPages = "text_pages"
        case visionPages = "vision_pages"
        case pages
    }
}

private struct Options {
    var mode = "text"
    var maxPages = 48
    var pageMaxChars = 12_000
    var inputPath = ""
}

private enum HelperError: LocalizedError {
    case invalidArguments(String)
    case openFailed(String)
    case pageFailed(Int)

    var errorDescription: String? {
        switch self {
        case .invalidArguments(let message):
            return message
        case .openFailed(let path):
            return "cannot open PDF: \(path)"
        case .pageFailed(let page):
            return "cannot render PDF page \(page)"
        }
    }
}

private func parseOptions() throws -> Options {
    var options = Options()
    var index = 1
    let arguments = CommandLine.arguments
    while index < arguments.count {
        let argument = arguments[index]
        switch argument {
        case "--mode":
            index += 1
            guard index < arguments.count else {
                throw HelperError.invalidArguments("--mode requires text or ocr")
            }
            options.mode = arguments[index]
        case "--max-pages":
            index += 1
            guard index < arguments.count, let value = Int(arguments[index]), value > 0 else {
                throw HelperError.invalidArguments("--max-pages requires a positive integer")
            }
            options.maxPages = value
        case "--page-max-chars":
            index += 1
            guard index < arguments.count, let value = Int(arguments[index]), value > 0 else {
                throw HelperError.invalidArguments("--page-max-chars requires a positive integer")
            }
            options.pageMaxChars = value
        default:
            if argument.hasPrefix("--") {
                throw HelperError.invalidArguments("unknown option: \(argument)")
            }
            options.inputPath = argument
        }
        index += 1
    }
    guard options.mode == "text" || options.mode == "ocr" else {
        throw HelperError.invalidArguments("--mode must be text or ocr")
    }
    guard !options.inputPath.isEmpty else {
        throw HelperError.invalidArguments("PDF path is required")
    }
    return options
}

private func normalize(_ text: String, maxChars: Int) -> String {
    var value = text.replacingOccurrences(of: "\r\n", with: "\n")
    value = value.replacingOccurrences(of: "\r", with: "\n")
    value = value.trimmingCharacters(in: .whitespacesAndNewlines)
    if value.count > maxChars {
        value = String(value.prefix(maxChars))
    }
    return value
}

private func meaningfulCount(_ text: String) -> Int {
    text.unicodeScalars.reduce(into: 0) { count, scalar in
        if !CharacterSet.whitespacesAndNewlines.contains(scalar) {
            count += 1
        }
    }
}

private func render(_ page: PDFPage, pageNumber: Int) throws -> CGImage {
    let bounds = page.bounds(for: .mediaBox)
    let scale: CGFloat = 2.0
    let width = max(1, Int(ceil(bounds.width * scale)))
    let height = max(1, Int(ceil(bounds.height * scale)))
    guard let context = CGContext(
        data: nil,
        width: width,
        height: height,
        bitsPerComponent: 8,
        bytesPerRow: 0,
        space: CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ) else {
        throw HelperError.pageFailed(pageNumber)
    }
    context.setFillColor(NSColor.white.cgColor)
    context.fill(CGRect(x: 0, y: 0, width: width, height: height))
    context.saveGState()
    context.scaleBy(x: scale, y: scale)
    context.translateBy(x: -bounds.minX, y: -bounds.minY)
    page.draw(with: .mediaBox, to: context)
    context.restoreGState()
    guard let image = context.makeImage() else {
        throw HelperError.pageFailed(pageNumber)
    }
    return image
}

private func recognize(_ image: CGImage) throws -> String {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    request.recognitionLanguages = ["zh-Hans", "zh-Hant", "en-US"]
    let handler = VNImageRequestHandler(cgImage: image, orientation: .up, options: [:])
    try handler.perform([request])
    let observations = (request.results ?? []).sorted { left, right in
        let verticalDifference = abs(left.boundingBox.midY - right.boundingBox.midY)
        if verticalDifference > 0.015 {
            return left.boundingBox.midY > right.boundingBox.midY
        }
        return left.boundingBox.minX < right.boundingBox.minX
    }
    return observations.compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
}

private func run() throws -> DocumentOutput {
    let options = try parseOptions()
    let url = URL(fileURLWithPath: options.inputPath)
    guard let document = PDFDocument(url: url) else {
        throw HelperError.openFailed(options.inputPath)
    }
    let totalPages = document.pageCount
    let processedPages = min(totalPages, options.maxPages)
    var pages: [PageOutput] = []
    var textPages = 0
    var visionPages = 0

    for index in 0..<processedPages {
        guard let page = document.page(at: index) else {
            continue
        }
        let embedded = normalize(page.string ?? "", maxChars: options.pageMaxChars)
        if meaningfulCount(embedded) >= 16 || options.mode == "text" {
            if !embedded.isEmpty {
                textPages += 1
                pages.append(PageOutput(number: index + 1, source: "pdfkit", text: embedded))
            }
            continue
        }
        let image = try render(page, pageNumber: index + 1)
        let recognized = normalize(try recognize(image), maxChars: options.pageMaxChars)
        if !recognized.isEmpty {
            visionPages += 1
            pages.append(PageOutput(number: index + 1, source: "vision", text: recognized))
        }
    }
    return DocumentOutput(
        totalPages: totalPages,
        processedPages: processedPages,
        textPages: textPages,
        visionPages: visionPages,
        pages: pages
    )
}

do {
    let result = try run()
    let encoder = JSONEncoder()
    let data = try encoder.encode(result)
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([0x0A]))
} catch {
    FileHandle.standardError.write(Data("\(error.localizedDescription)\n".utf8))
    exit(1)
}
