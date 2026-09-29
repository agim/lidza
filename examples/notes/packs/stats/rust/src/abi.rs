//! The Līdza WASM ABI: the host writes JSON into linear memory, calls an
//! export with (ptr, len), and reads JSON back from the buffer the export
//! returns as ptr << 32 | len. Both buffers come from `lidza_alloc` and go
//! back through `lidza_free`. Errors travel as `{"$error": "message"}`,
//! with `"$code"` when the capability names the kind of failure.
//!
//! A capability is one `lidza_export!(name, |input: In| -> Result<Out, String>)`.
//! Return `Result<Out, abi::Error>` instead to give an error a code the Go
//! side can match (`engine.ErrorCode(err)`), such as
//! `Error::code("unsupported_format", "HEIC images are not supported")`.

use std::alloc::{alloc, dealloc, Layout};

/// Allocates `len` bytes for the host to write into.
#[unsafe(no_mangle)]
pub extern "C" fn lidza_alloc(len: u32) -> u32 {
    let layout = Layout::from_size_align(len.max(1) as usize, 1).expect("layout");
    unsafe { alloc(layout) as u32 }
}

/// Frees a buffer from `lidza_alloc` or a result buffer.
#[unsafe(no_mangle)]
pub extern "C" fn lidza_free(ptr: u32, len: u32) {
    let layout = Layout::from_size_align(len.max(1) as usize, 1).expect("layout");
    unsafe { dealloc(ptr as *mut u8, layout) }
}

/// A capability's error: the message, and optionally a code naming the
/// kind of failure for the caller to branch on. A `String` or `&str`
/// converts into one without a code, so `?` works on either.
#[derive(Debug)]
pub struct Error {
    pub code: Option<String>,
    pub message: String,
}

impl Error {
    /// An error with a code (lowercase words joined by `_`).
    pub fn code(code: &str, message: impl Into<String>) -> Self {
        Error { code: Some(code.to_string()), message: message.into() }
    }
}

impl From<String> for Error {
    fn from(message: String) -> Self {
        Error { code: None, message }
    }
}

impl From<&str> for Error {
    fn from(message: &str) -> Self {
        Error { code: None, message: message.to_string() }
    }
}

/// Decodes the input, runs `f`, encodes the result. Used by `lidza_export!`.
pub fn call<I, O, E>(ptr: u32, len: u32, f: impl FnOnce(I) -> Result<O, E>) -> u64
where
    I: serde::de::DeserializeOwned,
    O: serde::Serialize,
    E: Into<Error>,
{
    let input = unsafe { std::slice::from_raw_parts(ptr as *const u8, len as usize) };
    let out = match serde_json::from_slice::<I>(input)
        .map_err(|e| Error::from(format!("invalid input: {e}")))
        .and_then(|i| f(i).map_err(Into::into))
    {
        Ok(v) => serde_json::to_vec(&v).unwrap_or_else(|e| error_json(&Error::from(e.to_string()))),
        Err(e) => error_json(&e),
    };
    pack(out)
}

fn error_json(e: &Error) -> Vec<u8> {
    let v = match &e.code {
        Some(code) => serde_json::json!({ "$error": e.message, "$code": code }),
        None => serde_json::json!({ "$error": e.message }),
    };
    serde_json::to_vec(&v).expect("error json")
}

/// Hands a buffer to the host: exact-size, freed by the host with lidza_free.
fn pack(v: Vec<u8>) -> u64 {
    let boxed = v.into_boxed_slice();
    let len = boxed.len() as u32;
    let ptr = Box::leak(boxed).as_ptr() as u32;
    ((ptr as u64) << 32) | len as u64
}

/// Declares a capability: an exported function taking and returning JSON.
#[macro_export]
macro_rules! lidza_export {
    ($name:ident, $f:expr) => {
        #[unsafe(no_mangle)]
        pub extern "C" fn $name(ptr: u32, len: u32) -> u64 {
            $crate::abi::call(ptr, len, $f)
        }
    };
}
