use std::fmt;

pub fn display(value: impl fmt::Display) -> String {
    value.to_string()
}
