//! The Līdza WASM ABI: the host writes JSON into linear memory, calls an
//! export with (ptr, len), and reads JSON back from the buffer the export
//! returns as ptr << 32 | len. Both buffers come from `lidza_alloc` and go
//! back through `lidza_free`. Errors travel as `{"$error": "message"}`.
//!
//! A capability is one `lidza_export!(name, |input: In| -> Result<Out, String>)`.

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

/// Decodes the input, runs `f`, encodes the result. Used by `lidza_export!`.
pub fn call<I, O>(ptr: u32, len: u32, f: impl FnOnce(I) -> Result<O, String>) -> u64
where
    I: serde::de::DeserializeOwned,
    O: serde::Serialize,
{
    let input = unsafe { std::slice::from_raw_parts(ptr as *const u8, len as usize) };
    let out = match serde_json::from_slice::<I>(input)
        .map_err(|e| format!("invalid input: {e}"))
        .and_then(f)
    {
        Ok(v) => serde_json::to_vec(&v).unwrap_or_else(|e| error_json(&e.to_string())),
        Err(e) => error_json(&e),
    };
    pack(out)
}

fn error_json(msg: &str) -> Vec<u8> {
    serde_json::to_vec(&serde_json::json!({ "$error": msg })).expect("error json")
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
