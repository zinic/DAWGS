package catalog

type Snapshot struct {
	Relations    []Relation
	Indexes      []Index
	Constraints  []Constraint
	Triggers     []Trigger
	Routines     []Routine
	Sequences    []Sequence
	Dependencies []Dependency
	Rules        []Rule
	Publications []Publication
	Memberships  []Membership
	RoleSettings []RoleSetting
	DefaultACLs  []DefaultACL
}

type Relation struct {
	OID               uint32
	Schema            string
	Name              string
	Owner             string
	Kind              string
	Persistence       string
	ReplicaIdentity   string
	Tablespace        string
	Comment           string
	RowSecurity       bool
	ForceRowSecurity  bool
	StorageParameters []string
	ParentOID         *uint32
	Partition         *Partition
	ACL               []ACL
}

type Partition struct {
	Strategy      string
	KeyDefinition string
	Bound         string
	Default       bool
}

type ACL struct {
	Grantor   string
	Grantee   string
	Privilege string
	Grantable bool
}

type Index struct {
	OID         uint32
	RelationOID uint32
	Name        string
	Unique      bool
	Primary     bool
	Valid       bool
	Ready       bool
	Definition  string
}

type Constraint struct {
	OID         uint32
	RelationOID uint32
	Name        string
	Type        string
	Validated   bool
	Deferrable  bool
	Deferred    bool
	Definition  string
}

type Trigger struct {
	OID            uint32
	RelationOID    uint32
	Name           string
	Enabled        string
	Internal       bool
	FunctionSchema string
	FunctionName   string
	Definition     string
}

type Routine struct {
	OID               uint32
	Schema            string
	Name              string
	Owner             string
	Kind              string
	SecurityDefiner   bool
	Volatility        string
	Parallel          string
	Configuration     []string
	IdentityArguments string
	Result            string
	Definition        string
	Extension         bool
	ACL               []ACL
}

type Sequence struct {
	OID       uint32
	Schema    string
	Name      string
	Owner     string
	DataType  string
	Start     int64
	Minimum   int64
	Maximum   int64
	Increment int64
	Cycle     bool
	Cache     int64
}

type Dependency struct {
	ClassOID            uint32
	ObjectOID           uint32
	ObjectSubID         int32
	ObjectType          string
	ObjectSchema        string
	ObjectName          string
	ObjectIdentity      string
	ReferencedClassOID  uint32
	ReferencedObjectOID uint32
	ReferencedSubID     int32
	ReferencedType      string
	ReferencedSchema    string
	ReferencedName      string
	ReferencedIdentity  string
	Type                string
}

type Rule struct {
	Schema     string
	Relation   string
	Name       string
	Definition string
}

type Publication struct {
	Name      string
	Owner     string
	AllTables bool
	Insert    bool
	Update    bool
	Delete    bool
	Truncate  bool
	ViaRoot   bool
}

type Membership struct {
	Role        string
	Member      string
	Grantor     string
	AdminOption bool
}

type RoleSetting struct {
	Database string
	Role     string
	Values   []string
}

type DefaultACL struct {
	Owner      string
	Schema     string
	ObjectType string
	ACL        []ACL
}
