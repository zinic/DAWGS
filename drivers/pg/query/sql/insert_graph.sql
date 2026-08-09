-- Creates a new graph and returns the resulting graph ID.
insert into public.graph (name)
values (@name)
returning id;
