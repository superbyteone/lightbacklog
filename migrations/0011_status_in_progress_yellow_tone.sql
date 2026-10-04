-- Adjust the "In Progress" yellow: #ca8a04 read as too brownish.
UPDATE statuses SET color = '#ffc100' WHERE slug = 'in_progress';
