//! Līdza compute core.
//!
//! `abi` is the JSON-over-memory convention every pack's WASM module
//! follows; `pkg/engine` on the Go side is its host.

pub mod abi;

/// The crate version, as a NUL-free ASCII string.
pub fn version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

/// Exported for the WASM targets so a module built from this crate has at
/// least one callable symbol to verify the toolchain with.
#[unsafe(no_mangle)]
pub extern "C" fn lidza_core_abi_version() -> u32 {
    1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_matches_manifest() {
        assert_eq!(version(), "0.1.0");
    }

    #[test]
    fn abi_version() {
        assert_eq!(lidza_core_abi_version(), 1);
    }
}
