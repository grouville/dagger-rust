pub fn value() -> u32 { 20 }
pub fn label() -> &'static str { option_env!("RCE_LABEL").unwrap_or("default") }
