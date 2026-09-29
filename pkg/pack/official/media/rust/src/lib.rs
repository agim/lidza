//! Official media pack: image inspection and resizing on the `image`
//! crate. Bytes cross the ABI as base64 (schema type `bytes`).
//!
//! Formats: jpeg, png, webp, gif and bmp in, jpeg, png and webp out. Any
//! other (HEIC and AVIF from phones, TIFF) fails with the error code
//! `unsupported_format`, which the Go side reads with
//! `engine.ErrorCode(err)`: the app keeps the original file then. HEIC
//! has no mature pure-Rust decoder, and the pack takes no C dependency.

pub mod abi;
pub mod schema;

use std::io::Cursor;

use abi::Error;
use image::{ImageError, ImageFormat, ImageReader};
use schema::{ImageInfo, ImageInfoInput, ImageResizeInput, ImageResult};

/// The code of an image the pack cannot decode.
const UNSUPPORTED: &str = "unsupported_format";

/// Names an ISO base media file the `image` crate has no decoder for, by
/// its ftyp brand: HEIC/HEIF and AVIF.
fn container_format(data: &[u8]) -> Option<&'static str> {
    if data.len() < 12 || &data[4..8] != b"ftyp" {
        return None;
    }
    match &data[8..12] {
        b"heic" | b"heix" | b"hevc" | b"hevx" | b"heim" | b"heis" | b"hevm" | b"hevs" | b"mif1" | b"msf1" => Some("heic"),
        b"avif" | b"avis" => Some("avif"),
        _ => None,
    }
}

fn reader(data: &[u8]) -> Result<(ImageReader<Cursor<&[u8]>>, ImageFormat), Error> {
    if let Some(name) = container_format(data) {
        return Err(Error::code(UNSUPPORTED, format!("{name} images are not supported; keep the original")));
    }
    let r = ImageReader::new(Cursor::new(data))
        .with_guessed_format()
        .map_err(|e| format!("unreadable image: {e}"))?;
    let format = r.format().ok_or_else(|| Error::code(UNSUPPORTED, "unknown image format; supported: jpeg, png, webp, gif, bmp"))?;
    Ok((r, format))
}

/// Turns a decoding error into the pack's error: a format the build has
/// no decoder for is unsupported_format, anything else a damaged file.
fn decode_error(what: &str, e: ImageError) -> Error {
    match e {
        ImageError::Unsupported(u) => Error::code(UNSUPPORTED, format!("{u}; supported: jpeg, png, webp, gif, bmp")),
        e => Error::from(format!("{what}: {e}")),
    }
}

fn format_name(f: ImageFormat) -> String {
    f.extensions_str().first().map(|s| s.to_string()).unwrap_or_else(|| "unknown".into())
}

lidza_export!(image_info, |input: ImageInfoInput| -> Result<ImageInfo, Error> {
    let (r, format) = reader(&input.data)?;
    let (width, height) = r.into_dimensions().map_err(|e| decode_error("cannot read dimensions", e))?;
    Ok(ImageInfo { width: width as i32, height: height as i32, format: format_name(format) })
});

lidza_export!(image_resize, |input: ImageResizeInput| -> Result<ImageResult, Error> {
    let (r, source) = reader(&input.data)?;
    let img = r.decode().map_err(|e| decode_error("cannot decode image", e))?;
    let (w, h) = (img.width(), img.height());
    let filter = image::imageops::FilterType::Lanczos3;
    let resized = match input.fit.as_deref() {
        // Cover: exactly width x height, scaled to fill and the overflow
        // cropped around the centre (a square avatar from a portrait).
        Some("cover") => {
            let (Some(cw), Some(ch)) = (input.width, input.height) else {
                return Err("fit cover needs both width and height".into());
            };
            let (cw, ch) = (cw.max(1) as u32, ch.max(1) as u32);
            if (cw, ch) == (w, h) { img } else { img.resize_to_fill(cw, ch, filter) }
        }
        // Contain: inside the box, aspect ratio kept, never enlarged.
        None | Some("contain") => {
            let max_w = input.width.map(|v| v.max(1) as u32).unwrap_or(w);
            let max_h = input.height.map(|v| v.max(1) as u32).unwrap_or(h);
            if max_w >= w && max_h >= h { img } else { img.resize(max_w, max_h, filter) }
        }
        Some(other) => return Err(format!("unknown fit {other}; use contain or cover").into()),
    };
    let format = match input.format.as_deref() {
        Some("png") => ImageFormat::Png,
        Some("webp") => ImageFormat::WebP,
        Some("jpeg") | Some("jpg") => ImageFormat::Jpeg,
        Some(other) => return Err(format!("unsupported output format {other}; use jpeg, png or webp").into()),
        None => match source {
            ImageFormat::Png | ImageFormat::WebP => source,
            _ => ImageFormat::Jpeg,
        },
    };
    let mut out = Cursor::new(Vec::new());
    match format {
        ImageFormat::Jpeg => {
            let quality = input.quality.unwrap_or(85).clamp(1, 100) as u8;
            let enc = image::codecs::jpeg::JpegEncoder::new_with_quality(&mut out, quality);
            resized.to_rgb8().write_with_encoder(enc).map_err(|e| format!("encode: {e}"))?;
        }
        _ => resized.write_to(&mut out, format).map_err(|e| format!("encode: {e}"))?,
    }
    Ok(ImageResult {
        data: out.into_inner(),
        width: resized.width() as i32,
        height: resized.height() as i32,
        format: format_name(format),
    })
});
