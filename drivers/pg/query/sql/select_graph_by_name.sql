-- Selects the ID of a graph with the given name.
select id
from public.graph
where name = @name;
