//! Official geo pack: haversine distance, nearest neighbours on an R-tree,
//! bounding boxes. Coordinates are WGS84 decimal degrees.

pub mod abi;
pub mod schema;

use rstar::{primitives::GeomWithData, RTree};
use schema::{GeoBbox, GeoBboxInput, GeoDistance, GeoDistanceInput, GeoNearest, GeoNearestInput, GeoNeighbor, GeoPoint};

const EARTH_RADIUS_M: f64 = 6_371_008.8;

fn haversine(a: &GeoPoint, b: &GeoPoint) -> f64 {
    let (lat1, lat2) = (a.lat.to_radians(), b.lat.to_radians());
    let dlat = lat2 - lat1;
    let dlng = (b.lng - a.lng).to_radians();
    let h = (dlat / 2.0).sin().powi(2) + lat1.cos() * lat2.cos() * (dlng / 2.0).sin().powi(2);
    2.0 * EARTH_RADIUS_M * h.sqrt().asin()
}

fn check(p: &GeoPoint) -> Result<(), String> {
    if !(-90.0..=90.0).contains(&p.lat) || !(-180.0..=180.0).contains(&p.lng) {
        return Err(format!("point out of range: lat {} lng {}", p.lat, p.lng));
    }
    Ok(())
}

lidza_export!(geo_distance, |input: GeoDistanceInput| -> Result<GeoDistance, String> {
    check(&input.from)?;
    check(&input.to)?;
    Ok(GeoDistance { meters: haversine(&input.from, &input.to) })
});

lidza_export!(geo_nearest, |input: GeoNearestInput| -> Result<GeoNearest, String> {
    check(&input.to)?;
    if input.points.is_empty() {
        return Err("points is empty".into());
    }
    // Planar index with longitude scaled by the query latitude: good for
    // ranking candidates; the reported distance is haversine.
    let scale = input.to.lat.to_radians().cos().max(1e-6);
    let key = |p: &GeoPoint| [p.lng * scale, p.lat];
    let items: Vec<_> = input
        .points
        .iter()
        .enumerate()
        .map(|(i, p)| GeomWithData::new(key(p), i))
        .collect();
    let tree = RTree::bulk_load(items);
    let query = key(&input.to);
    let limit = input.limit.unwrap_or(10).max(1) as usize;
    let mut points: Vec<GeoNeighbor> = tree
        .nearest_neighbor_iter(query)
        .take(limit.saturating_mul(2).min(input.points.len()))
        .map(|n| {
            let p = &input.points[n.data];
            GeoNeighbor { point: p.clone(), meters: haversine(p, &input.to) }
        })
        .collect();
    points.sort_by(|a, b| a.meters.total_cmp(&b.meters));
    points.truncate(limit);
    Ok(GeoNearest { points })
});

lidza_export!(geo_bbox, |input: GeoBboxInput| -> Result<GeoBbox, String> {
    if input.points.is_empty() {
        return Err("points is empty".into());
    }
    let mut b = GeoBbox { min_lat: 90.0, min_lng: 180.0, max_lat: -90.0, max_lng: -180.0, count: 0 };
    for p in &input.points {
        check(p)?;
        b.min_lat = b.min_lat.min(p.lat);
        b.max_lat = b.max_lat.max(p.lat);
        b.min_lng = b.min_lng.min(p.lng);
        b.max_lng = b.max_lng.max(p.lng);
        b.count += 1;
    }
    Ok(b)
});
