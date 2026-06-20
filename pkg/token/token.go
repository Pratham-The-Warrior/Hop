package token

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// tokenPattern matches the expected token format: word-word-NN
var tokenPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-\d{2}$`)

// words is a curated list of 4096 common, friendly English words used to
// generate memorable transfer tokens. The word-word-NN format provides
// approximately 36 bits of entropy (12 + 12 + 7 bits), making brute-force
// enumeration impractical within the token's 24-hour lifetime.
//
// Using a subset here for the initial build. A full 4096-word list would be
// loaded from an embedded file in production.
var words = []string{
	"able", "acid", "aged", "also", "area", "army", "away", "baby", "back", "ball",
	"band", "bank", "base", "bath", "bear", "beat", "been", "bell", "belt", "best",
	"bill", "bird", "bite", "blow", "blue", "boat", "body", "bomb", "bond", "bone",
	"book", "born", "boss", "both", "bowl", "bulk", "burn", "bush", "busy", "calm",
	"came", "camp", "card", "care", "case", "cash", "cast", "cell", "chat", "chip",
	"city", "claim", "clay", "clip", "club", "clue", "coal", "coat", "code", "cold",
	"come", "cook", "cool", "cope", "copy", "core", "cost", "crew", "crop", "crow",
	"cure", "cute", "dame", "dare", "dark", "data", "date", "dawn", "dead", "deaf",
	"deal", "dear", "debt", "deep", "deer", "deny", "desk", "dial", "diet", "dirt",
	"disc", "dish", "dock", "does", "done", "doom", "door", "dose", "down", "draw",
	"drew", "drop", "drug", "drum", "dual", "duke", "dull", "dune", "dust", "duty",
	"each", "earn", "ease", "east", "easy", "edge", "else", "even", "ever", "exam",
	"evil", "exit", "face", "fact", "fade", "fail", "fair", "fall", "fame", "farm",
	"fast", "fate", "fear", "feed", "feel", "feet", "fell", "felt", "file", "fill",
	"film", "find", "fine", "fire", "firm", "fish", "flag", "flat", "flew", "flip",
	"flow", "foam", "fold", "folk", "fond", "font", "food", "foot", "ford", "fore",
	"fork", "form", "fort", "foul", "four", "free", "from", "fuel", "full", "fund",
	"fury", "fuse", "gain", "gale", "game", "gang", "gate", "gave", "gaze", "gear",
	"gene", "gift", "girl", "give", "glad", "glow", "glue", "goat", "goes", "gold",
	"golf", "gone", "good", "grab", "gran", "gray", "grew", "grid", "grin", "grip",
	"grow", "gulf", "guru", "hack", "hair", "half", "hall", "halt", "hand", "hang",
	"harm", "hate", "haul", "have", "hawk", "head", "heal", "heap", "hear", "heat",
	"heel", "held", "hell", "help", "herb", "here", "hero", "hide", "high", "hike",
	"hill", "hint", "hire", "hold", "hole", "holy", "home", "hood", "hook", "hope",
	"horn", "host", "hour", "huge", "hull", "hung", "hunt", "hurt", "hymn", "icon",
	"idea", "inch", "info", "into", "iron", "isle", "item", "jack", "jade", "jail",
	"jazz", "jean", "jeep", "jerk", "jest", "jobs", "join", "joke", "jury", "just",
	"keen", "keep", "kept", "kick", "kill", "kind", "king", "kiss", "knee", "knew",
	"knit", "knot", "know", "lace", "lack", "lady", "laid", "lake", "lamp", "land",
	"lane", "lark", "last", "late", "lawn", "lead", "leaf", "lean", "leap", "left",
	"lend", "lens", "less", "lick", "life", "lift", "like", "limb", "lime", "limp",
	"line", "link", "lion", "list", "live", "load", "loan", "lock", "logo", "lone",
	"long", "look", "lord", "lose", "loss", "lost", "loud", "love", "luck", "lump",
	"lung", "lure", "lurk", "lush", "made", "mail", "main", "make", "male", "mall",
	"malt", "mane", "many", "maps", "mark", "mars", "mask", "mass", "mate", "maze",
	"meal", "mean", "meat", "meet", "melt", "memo", "menu", "mere", "mesh", "mild",
	"milk", "mill", "mind", "mine", "mint", "miss", "mist", "mock", "mode", "mold",
	"mood", "moon", "more", "moss", "most", "moth", "move", "much", "mule", "muse",
	"must", "myth", "nail", "name", "navy", "near", "neat", "neck", "need", "nest",
	"news", "next", "nice", "nine", "node", "none", "norm", "nose", "note", "noun",
	"nude", "nuts", "oaks", "oath", "obey", "odds", "oils", "okay", "once", "only",
	"onto", "open", "opts", "oral", "orca", "oven", "over", "owed", "owes", "owns",
	"pace", "pack", "page", "paid", "pain", "pair", "pale", "palm", "pane", "park",
	"part", "pass", "past", "path", "peak", "pear", "peel", "peer", "pick", "pier",
	"pile", "pine", "pink", "pipe", "plan", "play", "plea", "plot", "ploy", "plug",
	"plus", "poem", "poet", "poll", "polo", "pond", "pool", "poor", "pope", "pork",
	"port", "pose", "post", "pour", "pray", "prey", "prop", "pull", "pulp", "pump",
	"pure", "push", "quit", "quiz", "race", "rack", "rage", "raid", "rail", "rain",
	"rank", "rare", "rash", "rate", "rays", "read", "real", "rear", "reed", "reef",
	"rein", "rely", "rent", "rest", "rice", "rich", "ride", "rift", "ring", "rise",
	"risk", "road", "roam", "robe", "rock", "rode", "role", "roll", "roof", "room",
	"root", "rope", "rose", "ruin", "rule", "rush", "rust", "safe", "saga", "sage",
	"said", "sail", "sake", "sale", "salt", "same", "sand", "sang", "save", "seal",
	"seam", "seas", "seat", "seed", "seek", "seem", "seen", "self", "sell", "send",
	"sent", "sept", "sere", "sewn", "shed", "shin", "ship", "shop", "shot", "show",
	"shut", "sick", "side", "sigh", "sign", "silk", "sing", "sink", "site", "size",
	"skip", "slam", "slap", "slew", "slid", "slim", "slip", "slot", "slow", "slug",
	"snap", "snow", "soak", "soar", "sock", "soft", "soil", "sold", "sole", "some",
	"song", "soon", "sort", "soul", "sour", "span", "spec", "spin", "spit", "spot",
	"spur", "star", "stay", "stem", "step", "stew", "stop", "stud", "such", "suit",
	"sulk", "sung", "sure", "surf", "swan", "swap", "swim", "tack", "tail", "take",
	"tale", "talk", "tall", "tank", "tape", "task", "taxi", "teal", "team", "tear",
	"teen", "tell", "temp", "tend", "tent", "term", "test", "text", "than", "that",
	"them", "then", "they", "thin", "thus", "tick", "tide", "tidy", "tied", "ties",
	"tile", "till", "tilt", "time", "tiny", "tire", "toad", "told", "toll", "tomb",
	"tone", "took", "tool", "tops", "tore", "torn", "tour", "town", "tram", "trap",
	"tray", "tree", "trim", "trio", "trip", "trod", "true", "tube", "tuck", "tuna",
	"tune", "turf", "turn", "twin", "type", "ugly", "undo", "unit", "unto", "upon",
	"urge", "used", "user", "uses", "vain", "vale", "vary", "vast", "veil", "vein",
	"vent", "verb", "very", "vest", "veto", "vice", "view", "vine", "visa", "void",
	"volt", "vote", "wade", "wage", "wait", "wake", "walk", "wall", "wand", "ward",
	"warm", "warn", "warp", "wary", "wash", "vast", "wave", "wavy", "waxy", "weak",
	"weal", "wear", "weed", "week", "well", "went", "were", "west", "what", "when",
	"whom", "wick", "wide", "wife", "wild", "will", "wilt", "wily", "wind", "wine",
	"wing", "wink", "wire", "wise", "wish", "with", "woke", "wolf", "wood", "wool",
	"word", "wore", "work", "worm", "worn", "wove", "wrap", "wren", "yard", "yarn",
	"year", "yell", "yoga", "yoke", "your", "zeal", "zero", "zinc", "zone", "zoom",

	// Extended set to increase word pool
	"amber", "angel", "apple", "arrow", "azure", "beach", "berry", "birch", "blaze",
	"bloom", "blush", "brave", "brick", "brook", "brush", "candy", "cedar", "charm",
	"chase", "chess", "chief", "child", "cider", "cliff", "climb", "cloud", "clown",
	"coral", "crane", "crash", "cream", "creek", "crisp", "cross", "crowd", "crush",
	"curve", "cycle", "dance", "delta", "depot", "diary", "dizzy", "drift", "drink",
	"drive", "eagle", "earth", "ember", "fable", "fairy", "feast", "fence", "fiber",
	"field", "flame", "flash", "fleet", "float", "flock", "flood", "flour", "flute",
	"focus", "forge", "forum", "fresh", "frost", "fruit", "ghost", "giant", "glass",
	"gleam", "globe", "glory", "grace", "grain", "grape", "grass", "grave", "green",
	"grove", "guard", "guest", "guide", "haven", "heart", "honey", "horse", "house",
	"ivory", "jewel", "juice", "knife", "laser", "latch", "lemon", "light", "linen",
	"lunar", "magic", "manor", "maple", "marsh", "medal", "melon", "metal", "minor",
	"model", "money", "moose", "motor", "mount", "mouse", "mural", "music", "night",
	"noble", "noise", "north", "novel", "ocean", "olive", "opera", "orbit", "order",
	"other", "outer", "oxide", "paint", "panel", "paper", "party", "patch", "peace",
	"peach", "pearl", "penny", "petal", "phase", "phone", "photo", "piano", "pilot",
	"pixel", "place", "plain", "plane", "plant", "plate", "plaza", "plumb", "plume",
	"point", "polar", "porch", "power", "press", "price", "pride", "prime", "print",
	"prior", "prize", "proof", "pulse", "queen", "quest", "quiet", "quill", "radar",
	"radio", "ranch", "rapid", "raven", "reach", "realm", "rebel", "reign", "rider",
	"ridge", "ripen", "rival", "river", "robin", "rodeo", "round", "route", "royal",
	"scene", "scope", "scout", "shade", "shall", "shape", "share", "shark", "sharp",
	"sheet", "shelf", "shell", "shift", "shire", "shore", "shout", "sight", "since",
	"skate", "skill", "skull", "slate", "sleep", "slide", "slope", "smile", "smoke",
	"snail", "snake", "solar", "solid", "solve", "sonic", "south", "space", "spade",
	"spark", "spear", "spice", "spike", "spine", "spoke", "spore", "spray", "squad",
	"stack", "staff", "stage", "stair", "stake", "stamp", "stand", "stark", "state",
	"steam", "steel", "steep", "steer", "stick", "still", "stock", "stone", "store",
	"storm", "story", "stove", "strap", "straw", "stray", "strip", "style", "sugar",
	"suite", "sunny", "surge", "swamp", "sweet", "swept", "swift", "swing", "sword",
	"syrup", "table", "these", "thick", "thing", "think", "thorn", "three", "throw",
	"thumb", "tiger", "title", "toast", "token", "torch", "total", "touch", "tower",
	"track", "trade", "trail", "train", "trait", "trash", "treat", "trend", "trial",
	"tribe", "trick", "trout", "truck", "truly", "trunk", "trust", "truth", "tulip",
	"twist", "ultra", "under", "upper", "urban", "usage", "usual", "valid", "valor",
	"value", "vapor", "vault", "verse", "vigor", "viral", "vivid", "vocal", "voice",
	"waste", "watch", "water", "wheat", "wheel", "where", "while", "white", "whole",
	"witch", "world", "worth", "wound", "wrist", "yield", "young", "youth",
}

// Generate creates a new transfer token in the format "word-word-NN"
// using cryptographically secure random number generation.
func Generate() string {
	w1 := randomWord()
	w2 := randomWord()
	// Ensure the two words are different
	for w2 == w1 {
		w2 = randomWord()
	}
	num := randomInt(100) // 00-99
	return fmt.Sprintf("%s-%s-%02d", w1, w2, num)
}

// Validate checks if a string matches the expected token format.
func Validate(tok string) bool {
	return tokenPattern.MatchString(tok)
}

// ParseToken splits a token into its components.
func ParseToken(tok string) (word1, word2 string, number int, ok bool) {
	parts := strings.SplitN(tok, "-", 3)
	if len(parts) != 3 {
		return "", "", 0, false
	}
	var n int
	_, err := fmt.Sscanf(parts[2], "%d", &n)
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], parts[1], n, true
}

// randomWord picks a random word from the word list.
func randomWord() string {
	idx := randomInt(len(words))
	return words[idx]
}

// randomInt returns a cryptographically secure random int in [0, max).
func randomInt(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		// Fallback should never happen with crypto/rand
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return int(n.Int64())
}
