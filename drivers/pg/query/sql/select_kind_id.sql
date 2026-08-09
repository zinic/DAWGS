-- Selects the ID of a given Kind by name
select id
from public.kind
where name = @name;
