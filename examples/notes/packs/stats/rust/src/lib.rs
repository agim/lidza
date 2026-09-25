//! Pack stats: word statistics for a note. One capability, text_stats:
//! word and unique-word counts, reading time at 200 words a minute, the
//! most frequent words. The input and output types come from
//! schema.lidza (the generated `schema` module); the Go side calls it as
//! `stats.From(ctx).TextStats(ctx, in)` through the generated pack.go.
//!
//! Why Rust here: one CPU pass over the text with a hash map, no I/O, and
//! the note body is user input, so a runaway is contained by the pool's
//! deadline and memory cap. The loop is bounded by the input, so the
//! manifest marks the pack uninterruptible (no per-loop deadline checks).
//!
//! After editing: `lidza pack build stats` (lidza dev does it on save).

pub mod abi;
pub mod schema;

use std::collections::HashMap;

use schema::{TextStats, TextStatsInput, WordCount};

const WORDS_PER_MINUTE: f64 = 200.0;

lidza_export!(text_stats, |input: TextStatsInput| -> Result<TextStats, String> {
    let mut counts: HashMap<String, i64> = HashMap::new();
    let mut words: i64 = 0;
    for word in input
        .text
        .split(|c: char| !c.is_alphanumeric() && c != '\'')
        .filter(|w| !w.is_empty())
    {
        words += 1;
        *counts.entry(word.to_lowercase()).or_insert(0) += 1;
    }
    let mut top_words: Vec<WordCount> = counts
        .iter()
        .map(|(word, count)| WordCount { word: word.clone(), count: *count as _ })
        .collect();
    top_words.sort_by(|a, b| b.count.cmp(&a.count).then(a.word.cmp(&b.word)));
    top_words.truncate(input.top.unwrap_or(5).clamp(1, 50) as usize);
    Ok(TextStats {
        words: words as _,
        unique: counts.len() as _,
        reading_minutes: words as f64 / WORDS_PER_MINUTE,
        top_words,
    })
});
