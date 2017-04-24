package app

import (
	"database/sql"
	"fmt"
	"github.com/kardianos/osext"
	"github.com/patrickmn/go-cache"
	"github.com/raggaer/castro/app/database"
	"github.com/raggaer/castro/app/lua"
	"github.com/raggaer/castro/app/models"
	"github.com/raggaer/castro/app/util"
	glua "github.com/yuin/gopher-lua"
	"html/template"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Start the main execution point for Castro
func Start() {
	// Wait for all goroutines to make their work
	wait := &sync.WaitGroup{}

	// Wait for all tasks
	wait.Add(7)

	// Load application logger
	loadAppLogger()

	// Load application config
	loadAppConfig()

	// Run logger renew service
	go util.RenewLogger()

	// Execute our tasks
	go func(wait *sync.WaitGroup) {

		loadLUAConfig()
		connectDatabase()
		loadMap()
		go loadHouses(wait)
		go loadVocations(wait)
	}(wait)

	// Create application cache
	createCache()

	go loadWidgetList(wait)
	go appTemplates(wait)
	go widgetTemplates(wait)
	go loadSubtopics(wait)
	go loadWidgets(wait)

	// Wait for the tasks
	wait.Wait()

	// Execute migrations
	executeMigrations()

	// Execute the init lua file
	executeInitFile()
}

func loadMap() {
	// Map holder
	m := models.Map{}

	// Check if map is encoded
	err := database.DB.Get(&m, "SELECT id, name, data, created_at, updated_at FROM castro_map WHERE name = ?", lua.Config.GetGlobal("mapName").String())

	if err != nil && err != sql.ErrNoRows {
		util.Logger.Fatalf("Cannot retrieve map from database: %v", err)
	}

	// Check if map is not encoded
	if err == sql.ErrNoRows {

		fmt.Println(">> Encoding map. This process can take several minutes")

		// Encode map
		mapData, err := util.EncodeMap(
			filepath.Join(util.Config.Datapack, "data", "world", lua.Config.GetGlobal("mapName").String()+".otbm"),
		)

		if err != nil {
			util.Logger.Fatalf("Cannot encode map file: %v", err)
		}

		// Update map struct
		m.Name = lua.Config.GetGlobal("mapName").String()
		m.Data = mapData
		m.Created_at = time.Now()
		m.Updated_at = time.Now()

		// Save map
		if _, err := database.DB.Exec("INSERT INTO castro_map (name, data, created_at, updated_at) VALUES (?, ?, ?, ?)", m.Name, m.Data, m.Created_at, m.Updated_at); err != nil {
			util.Logger.Fatalf("Cannot save encoded map file: %v", err)
		}
	}

	// Check if map is old
	if time.Now().Add(time.Hour * 24).Before(m.Updated_at) {

		// Encode map
		mapData, err := util.EncodeMap(
			filepath.Join(util.Config.Datapack, "data", "world", lua.Config.GetGlobal("mapName").String()+".otbm"),
		)

		if err != nil {
			util.Logger.Fatalf("Cannot encode map file: %v", err)
		}

		// Update map struct
		m.Name = lua.Config.GetGlobal("mapName").String()
		m.Data = mapData
		m.Created_at = time.Now()
		m.Updated_at = time.Now()

		// Save map
		if _, err := database.DB.Exec("INSERT INTO castro_map (name, data, created_at, updated_at) VALUES (?, ?, ?, ?)", m.Name, m.Data, m.Created_at, m.Updated_at); err != nil {
			util.Logger.Fatalf("Cannot save encoded map file: %v", err)
		}
	}

	// Decode map
	castroMap, err := util.DecodeMap(m.Data)

	if err != nil {
		util.Logger.Fatalf("Cannot decode map file: %v", err)
	}

	// Set map global
	util.OTBMap = castroMap
}

func executeMigrations() {
	// Create migration state
	state := glua.NewState()

	// Set database metatable
	lua.SetDatabaseMetaTable(state)

	// Close state
	defer state.Close()

	// Walk migrations directory
	if err := filepath.Walk("migrations", func(path string, info os.FileInfo, err error) error {

		// Check if lua file
		if !strings.HasSuffix(path, ".lua") {
			return nil
		}

		// Do lua file
		if err := state.DoFile(path); err != nil {
			return err
		}

		// Call migration function
		if err := state.CallByParam(
			glua.P{
				Fn:      state.GetGlobal("migration"),
				NRet:    0,
				Protect: !util.Config.IsDev(),
			},
		); err != nil {
			return err
		}

		// Pop state
		state.Pop(-1)

		return nil

	}); err != nil {

		util.Logger.Fatalf("Cannot run migration files: %v", err)
	}
}

func executeInitFile() {
	// Get lua state
	luaState := glua.NewState()

	// Close state
	defer luaState.Close()

	// Create http metatable
	lua.SetHTTPMetaTable(luaState)

	// Create env metatable
	lua.SetEnvMetaTable(luaState)

	// Create events metatable
	lua.SetEventsMetaTable(luaState)

	// Create storage metatable
	lua.SetStorageMetaTable(luaState)

	// Create time metatable
	lua.SetTimeMetaTable(luaState)

	// Create url metatable
	lua.SetURLMetaTable(luaState)

	// Create debug metatable
	lua.SetDebugMetaTable(luaState)

	// Create XML metatable
	lua.SetXMLMetaTable(luaState)

	// Create captcha metatable
	lua.SetCaptchaMetaTable(luaState)

	// Create crypto metatable
	lua.SetCryptoMetaTable(luaState)

	// Create validator metatable
	lua.SetValidatorMetaTable(luaState)

	// Create database metatable
	lua.SetDatabaseMetaTable(luaState)

	// Create config metatable
	lua.SetConfigMetaTable(luaState)

	// Create map metatable
	lua.SetMapMetaTable(luaState)

	// Create mail metatable
	lua.SetMailMetaTable(luaState)

	// Create cache metatable
	lua.SetCacheMetaTable(luaState)

	// Create reflect metatable
	lua.SetReflectMetaTable(luaState)

	// Create json metatable
	lua.SetJSONMetaTable(luaState)

	lua.SetConfigGlobal(luaState)

	// Get executable folder
	f, err := osext.ExecutableFolder()

	if err != nil {
		util.Logger.Fatalf("Cannot get executable folder path: %v", err)
	}

	// Get package metatable
	pkg := luaState.GetGlobal("package")

	// Set path field
	luaState.SetField(
		pkg,
		"path",
		glua.LString(
			filepath.Join(f, "engine", "?.lua"),
		),
	)

	// Execute init file
	if err := luaState.DoFile(filepath.Join("engine", "init.lua")); err != nil {
		util.Logger.Fatalf("Cannot execute init lua file: %v", err)
	}
}

func loadWidgets(wg *sync.WaitGroup) {
	// Load subtopic list
	if err := lua.WidgetList.Load("widgets"); err != nil {
		util.Logger.Fatalf("Cannot load application widget list: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func loadSubtopics(wg *sync.WaitGroup) {
	// Load subtopic list
	if err := lua.PageList.Load("pages"); err != nil {
		util.Logger.Fatalf("Cannot load application subtopic list: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func loadAppLogger() {
	// Create logger file
	f, day, err := util.CreateLogFile()

	if err != nil {
		log.Fatal(err)
	}

	// Set logger output variable
	util.LoggerOutput = f

	// Set last logger day
	util.LastLoggerDay = day

	// Create main application logger instance
	util.Logger = util.CreateLogger(f)
}

func loadVocations(wg *sync.WaitGroup) {
	// Load server vocations
	if err := util.LoadVocations(
		filepath.Join(util.Config.Datapack, "data", "XML", "vocations.xml"),
		util.ServerVocationList,
	); err != nil {
		util.Logger.Fatalf("Cannot load map house list: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func loadHouses(wg *sync.WaitGroup) {
	// Load server houses
	if err := util.LoadHouses(
		filepath.Join(util.Config.Datapack, "data", "world", util.OTBMap.HouseFile),
		util.ServerHouseList,
	); err != nil {
		util.Logger.Fatalf("Cannot load map house list: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func loadAppConfig() {
	// Load the TOML configuration file
	if err := util.LoadConfig("config.toml", util.Config); err != nil {
		util.Logger.Fatalf("Cannot read configuration file: %v", err)
	}
}

func loadLUAConfig() {
	// Load the LUA configuration file
	if err := lua.LoadConfig(filepath.Join(util.Config.Datapack, "config.lua")); err != nil {
		util.Logger.Fatalf("Cannot read lua configuration file: %v", err)
	}
}

func createCache() {
	// Create a new cache instance with the given options
	// first parameter is the default item duration on the cache
	// second parameter is the tick time to purge all dead cache items
	util.Cache = cache.New(util.Config.Cache.Default, util.Config.Cache.Purge)
}

func loadWidgetList(wg *sync.WaitGroup) {
	// Load widget list
	if err := util.Widgets.Load("widgets/"); err != nil {
		util.Logger.Fatalf("Cannot load widget list: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func appTemplates(wg *sync.WaitGroup) {
	// Create application template
	util.Template = util.NewTemplate("castro")

	// Set template functions
	util.Template.FuncMap(templateFuncs())
	util.FuncMap = templateFuncs()

	// Load templates
	if err := util.Template.LoadTemplates("views/"); err != nil {
		util.Logger.Fatalf("Cannot load templates: %v", err)
	}

	// Load subtopic templates
	if err := util.Template.LoadTemplates("pages/"); err != nil {
		util.Logger.Error(err.Error())
		return
	}

	// Tell the wait group we are done
	wg.Done()
}

func widgetTemplates(wg *sync.WaitGroup) {
	// Create widget template
	util.WidgetTemplate = util.NewTemplate("widget")

	util.WidgetTemplate.FuncMap(templateFuncs())

	// Load widget templates
	if err := util.WidgetTemplate.LoadTemplates("widgets/"); err != nil {
		util.Logger.Fatalf("Cannot load widget templates: %v", err)
	}

	// Tell the wait group we are done
	wg.Done()
}

func connectDatabase() {
	var err error

	// Connect to the MySQL database
	if database.DB, err = database.Open(lua.Config.GetGlobal("mysqlUser").String(), lua.Config.GetGlobal("mysqlPass").String(), lua.Config.GetGlobal("mysqlDatabase").String()); err != nil {
		util.Logger.Fatalf("Cannot connect to MySQL database: %v", err)
	}
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"vocation": func(voc float64) string {
			for _, v := range util.ServerVocationList.List.Vocations {
				if v.ID == int(voc) {
					return v.Name
				}
			}
			return ""
		},
		"isMap": func(i interface{}) bool {
			return reflect.TypeOf(i).Kind() == reflect.Map
		},
		"isDev": func() bool {
			return util.Config.IsDev()
		},
		"safeURL": func(s string) template.URL {
			return template.URL(s)
		},
		"url": func(args ...interface{}) template.URL {
			u := fmt.Sprintf("%v", util.Config.URL)
			for _, arg := range args {
				u = u + fmt.Sprintf("/%v", arg)
			}
			if util.Config.SSL.Proxy {
				return template.URL("https://" + u)
			}
			if util.Config.SSL.Enabled {
				return template.URL("https://" + u)
			}
			return template.URL("http://" + u)
		},
		"queryResults": func(m map[string]interface{}) []interface{} {
			n := len(m)
			r := []interface{}{}
			for i := 0; i < n; i++ {
				r = append(r, m[strconv.Itoa(i+1)])
			}
			return r
		},
		"unixToDate": func(m float64) template.HTML {
			date := time.Unix(int64(m), 0)
			return template.HTML(
				date.Format("2006 - Mon Jan 2 15:04:05"),
			)
		},
		"nl2br": func(text string) template.HTML {
			return template.HTML(
				strings.Replace(text, "\r\n", "<br>", -1),
			)
		},
		"urlEncode": func(t string) template.URL {
			return template.URL(url.QueryEscape(t))
		},
		"serverName": func() string {
			return lua.Config.GetGlobal("serverName").String()
		},
		"serverMotd": func() string {
			return lua.Config.GetGlobal("motd").String()
		},
		"widgetList": func() []*util.Widget {
			return util.Widgets.List
		},
		"captchaKey": func() string {
			return util.Config.Captcha.Public
		},
		"captchaEnabled": func() bool {
			return util.Config.Captcha.Enabled
		},
		"eqNumber": func(a, b float64) bool {
			return a == b
		},
	}
}
