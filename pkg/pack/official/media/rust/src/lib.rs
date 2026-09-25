//! Official media pack: image inspection and resizing on the `image`
//! crate. Bytes cross the ABI as base64 (schema type `bytes`).

pub mod abi;
pub mod schema;

use std::io::Cursor;

use image::{ImageFormat, ImageReader};
use schema::{ImageInfo, ImageInfoInput, ImageResizeInput, ImageResult};

fn reader(data: &[u8]) -> Result<ImageReader<Cursor<&[u8]>>, String> {
    ImageReader::new(Cursor::new(data))
        .with_guessed_format()
        .map_err(|e| format!("unreadable image: {e}"))
}

fn format_name(f: ImageFormat) -> String {
    f.extensions_str().first().map(|s| s.to_string()).unwrap_or_else(|| "unknown".into())
}

lidza_export!(image_info, |input: ImageInfoInput| -> Result<ImageInfo, String> {
    let r = reader(&input.data)?;
    let format = r.format().ok_or("unknown image format")?;
    let (width, height) = r.into_dimensions().map_err(|e| format!("cannot read dimensions: {e}"))?;
    Ok(ImageInfo { width: width as i32, height: height as i32, format: format_name(format) })
});

lidza_export!(image_resize, |input: ImageResizeInput| -> Result<ImageResult, String> {
    let r = reader(&input.data)?;
    let source = r.format().ok_or("unknown image format")?;
    let img = r.decode().map_err(|e| format!("cannot decode image: {e}"))?;
    let (w, h) = (img.width(), img.height());
    let max_w = input.width.map(|v| v.max(1) as u32).unwrap_or(w);
    let max_h = input.height.map(|v| v.max(1) as u32).unwrap_or(h);
    let resized = if max_w >= w && max_h >= h { img } else { img.resize(max_w, max_h, image::imageops::FilterType::Lanczos3) };
    let format = match input.format.as_deref() {
        Some("png") => ImageFormat::Png,
        Some("webp") => ImageFormat::WebP,
        Some("jpeg") | Some("jpg") => ImageFormat::Jpeg,
        Some(other) => return Err(format!("unsupported output format {other}; use jpeg, png or webp")),
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
