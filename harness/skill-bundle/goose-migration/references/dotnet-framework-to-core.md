# .NET Framework 4.x → .NET 8: Migration Reference

---
name: dotnet-framework-to-core
description: Migration patterns for .NET Framework 4.x (ASP.NET MVC/Web API) to .NET 8 (ASP.NET Core)
applies_to:
  manifests:
    csproj: true
  graph_patterns:
    - "imports contains System.Web"
    - "imports contains System.Web.Mvc"
    - "imports contains System.Web.Http"
---

## Migration Order (Layer Dependency)

Always migrate in this order — dependencies flow upward:

1. **Build config** - .csproj (SDK-style project, TargetFramework)
2. **App config** - appsettings.json (replaces Web.config)
3. **Startup** - Program.cs / Startup.cs (replaces Global.asax)
4. **Models / DTOs** - Data models (minimal changes)
5. **Data layer** - DbContext, repositories (EF6 → EF Core)
6. **Services** - Business logic (dependency injection changes)
7. **Controllers** - MVC/API controllers (namespace changes)
8. **Views** - Razor views (if applicable, minimal changes)
9. **Middleware** - Custom HTTP modules → middleware
10. **Cleanup** - Delete Web.config, Global.asax, packages.config

**Why this order**: Project file must be SDK-style first. Configuration and startup are foundational. Models have no deps. Data layer depends on models. Services depend on data. Controllers depend on services.

---

## Import/Package Transformations

All `System.Web.*` namespaces change to `Microsoft.AspNetCore.*`:

| Old (.NET Framework) | New (.NET 8) | Affected Components |
|----------------------|--------------|---------------------|
| `System.Web.Mvc` | `Microsoft.AspNetCore.Mvc` | MVC controllers, views |
| `System.Web.Http` | `Microsoft.AspNetCore.Mvc` | Web API controllers |
| `System.Web.Routing` | `Microsoft.AspNetCore.Routing` | Route configuration |
| `System.Web.Optimization` | Custom bundler (Webpack, Vite) | Bundling and minification |
| `System.Web.Helpers` | `Microsoft.AspNetCore.Mvc.ViewFeatures` | HTML helpers |
| `System.Web.Security` | `Microsoft.AspNetCore.Identity` | Authentication, authorization |
| `System.Configuration` | `Microsoft.Extensions.Configuration` | App settings |
| `System.Web.HttpContext` | `Microsoft.AspNetCore.Http.HttpContext` | HTTP context |

**Note**: Entity Framework 6 → Entity Framework Core requires additional changes (see Pattern 5).

---

## Annotation/Attribute Transformations

| Old (.NET Framework) | New (.NET 8) | Notes |
|----------------------|--------------|-------|
| `[RoutePrefix("api/users")]` | `[Route("api/users")]` | Both work, but `[Route]` is standard |
| `[HttpGet]` | `[HttpGet]` | No change (same attribute) |
| `[Authorize]` | `[Authorize]` | No change, but policy syntax updated |
| `[ValidateAntiForgeryToken]` | `[ValidateAntiForgeryToken]` | No change |

**Key difference**: Attributes are the same, but namespaces differ.

---

## Pattern Catalog

### Pattern 1: MVC Controller (System.Web.Mvc → ASP.NET Core MVC)

**BEFORE (.NET Framework 4.x):**
```csharp
using System.Web.Mvc;

namespace MyApp.Controllers
{
    public class HomeController : Controller
    {
        public ActionResult Index()
        {
            ViewBag.Message = "Welcome";
            return View();
        }
        
        [HttpPost]
        public ActionResult Create(User user)
        {
            if (ModelState.IsValid)
            {
                // Save user
                return RedirectToAction("Index");
            }
            return View(user);
        }
    }
}
```

**AFTER (.NET 8):**
```csharp
using Microsoft.AspNetCore.Mvc;

namespace MyApp.Controllers
{
    public class HomeController : Controller
    {
        public IActionResult Index()
        {
            ViewBag.Message = "Welcome";
            return View();
        }
        
        [HttpPost]
        public IActionResult Create(User user)
        {
            if (ModelState.IsValid)
            {
                // Save user
                return RedirectToAction("Index");
            }
            return View(user);
        }
    }
}
```

**Specific changes:**
1. Replace: `using System.Web.Mvc` → `using Microsoft.AspNetCore.Mvc`
2. Replace: `ActionResult` → `IActionResult` (preferred in .NET Core)
3. Keep: Everything else is identical (View(), RedirectToAction(), ModelState)

**Why**: ASP.NET Core unifies MVC and Web API into a single framework under `Microsoft.AspNetCore.Mvc`.

---

### Pattern 2: Web API Controller (System.Web.Http → ASP.NET Core)

**BEFORE (.NET Framework 4.x):**
```csharp
using System.Web.Http;
using System.Collections.Generic;

namespace MyApp.Controllers
{
    [RoutePrefix("api/users")]
    public class UsersController : ApiController
    {
        [HttpGet]
        [Route("")]
        public IHttpActionResult GetAll()
        {
            var users = _userService.GetAll();
            return Ok(users);
        }
        
        [HttpGet]
        [Route("{id}")]
        public IHttpActionResult GetById(int id)
        {
            var user = _userService.GetById(id);
            if (user == null)
                return NotFound();
            return Ok(user);
        }
        
        [HttpPost]
        [Route("")]
        public IHttpActionResult Create([FromBody] User user)
        {
            if (!ModelState.IsValid)
                return BadRequest(ModelState);
            
            _userService.Create(user);
            return Created($"api/users/{user.Id}", user);
        }
    }
}
```

**AFTER (.NET 8):**
```csharp
using Microsoft.AspNetCore.Mvc;
using System.Collections.Generic;

namespace MyApp.Controllers
{
    [ApiController]
    [Route("api/users")]
    public class UsersController : ControllerBase
    {
        private readonly IUserService _userService;
        
        public UsersController(IUserService userService)
        {
            _userService = userService;
        }
        
        [HttpGet]
        public IActionResult GetAll()
        {
            var users = _userService.GetAll();
            return Ok(users);
        }
        
        [HttpGet("{id}")]
        public IActionResult GetById(int id)
        {
            var user = _userService.GetById(id);
            if (user == null)
                return NotFound();
            return Ok(user);
        }
        
        [HttpPost]
        public IActionResult Create([FromBody] User user)
        {
            if (!ModelState.IsValid)
                return BadRequest(ModelState);
            
            _userService.Create(user);
            return CreatedAtAction(nameof(GetById), new { id = user.Id }, user);
        }
    }
}
```

**Specific changes:**
1. Replace: `using System.Web.Http` → `using Microsoft.AspNetCore.Mvc`
2. Replace: `ApiController` base class → `ControllerBase`
3. Add: `[ApiController]` attribute on controller class
4. Replace: `[RoutePrefix("api/users")]` → `[Route("api/users")]`
5. Simplify: `[Route("{id}")]` instead of separate attribute
6. Replace: `IHttpActionResult` → `IActionResult`
7. Replace: `Created(...)` → `CreatedAtAction(...)`
8. Add: Constructor-based dependency injection (required in ASP.NET Core)

**Why**: ASP.NET Core uses a unified controller model. Dependency injection is built-in, not optional.

---

### Pattern 3: Global.asax → Program.cs / Startup.cs

**BEFORE (.NET Framework 4.x - Global.asax.cs):**
```csharp
using System.Web;
using System.Web.Mvc;
using System.Web.Routing;
using System.Web.Http;

namespace MyApp
{
    public class MvcApplication : HttpApplication
    {
        protected void Application_Start()
        {
            AreaRegistration.RegisterAllAreas();
            GlobalConfiguration.Configure(WebApiConfig.Register);
            RouteConfig.RegisterRoutes(RouteTable.Routes);
            FilterConfig.RegisterGlobalFilters(GlobalFilters.Filters);
            BundleConfig.RegisterBundles(BundleTable.Bundles);
        }
        
        protected void Application_Error()
        {
            var exception = Server.GetLastError();
            // Log exception
        }
    }
}
```

**AFTER (.NET 8 - Program.cs - Minimal API style):**
```csharp
using Microsoft.AspNetCore.Builder;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

var builder = WebApplication.CreateBuilder(args);

// Add services to the container
builder.Services.AddControllersWithViews();
builder.Services.AddScoped<IUserService, UserService>();  // DI registration

var app = builder.Build();

// Configure the HTTP request pipeline
if (app.Environment.IsDevelopment())
{
    app.UseDeveloperExceptionPage();
}
else
{
    app.UseExceptionHandler("/Home/Error");
    app.UseHsts();
}

app.UseHttpsRedirection();
app.UseStaticFiles();
app.UseRouting();
app.UseAuthorization();

app.MapControllerRoute(
    name: "default",
    pattern: "{controller=Home}/{action=Index}/{id?}");

app.Run();
```

**OR (Startup.cs - traditional style):**
```csharp
// Program.cs
var builder = WebApplication.CreateBuilder(args);
var startup = new Startup(builder.Configuration);
startup.ConfigureServices(builder.Services);
var app = builder.Build();
startup.Configure(app, app.Environment);
app.Run();

// Startup.cs
public class Startup
{
    public IConfiguration Configuration { get; }
    
    public Startup(IConfiguration configuration)
    {
        Configuration = configuration;
    }
    
    public void ConfigureServices(IServiceCollection services)
    {
        services.AddControllersWithViews();
        services.AddScoped<IUserService, UserService>();
    }
    
    public void Configure(IApplicationBuilder app, IWebHostEnvironment env)
    {
        if (env.IsDevelopment())
        {
            app.UseDeveloperExceptionPage();
        }
        else
        {
            app.UseExceptionHandler("/Home/Error");
            app.UseHsts();
        }
        
        app.UseHttpsRedirection();
        app.UseStaticFiles();
        app.UseRouting();
        app.UseAuthorization();
        app.UseEndpoints(endpoints =>
        {
            endpoints.MapControllerRoute(
                name: "default",
                pattern: "{controller=Home}/{action=Index}/{id?}");
        });
    }
}
```

**Specific changes:**
1. Remove: `Global.asax` / `Global.asax.cs` files entirely
2. Create: `Program.cs` (application entry point)
3. Replace: `Application_Start()` → `builder.Services.Add*()` + `app.Use*()`
4. Replace: Route registration → `app.MapControllerRoute()`
5. Replace: Filter registration → Middleware pipeline (`app.Use*()`)
6. Replace: Bundle registration → External bundler (Webpack, Vite) or `UseStaticFiles()`
7. Add: Dependency injection in `ConfigureServices()`

**Why**: ASP.NET Core uses a built-in DI container and middleware pipeline. Global.asax is replaced by `Program.cs` and optional `Startup.cs`.

---

### Pattern 4: Web.config → appsettings.json

**BEFORE (.NET Framework 4.x - Web.config):**
```xml
<?xml version="1.0"?>
<configuration>
  <connectionStrings>
    <add name="DefaultConnection"
         connectionString="Server=localhost;Database=MyDb;Trusted_Connection=True;"
         providerName="System.Data.SqlClient" />
  </connectionStrings>
  
  <appSettings>
    <add key="AppName" value="MyApp" />
    <add key="MaxUploadSize" value="10485760" />
  </appSettings>
  
  <system.web>
    <compilation debug="true" targetFramework="4.8" />
    <httpRuntime targetFramework="4.8" />
    <authentication mode="Forms">
      <forms loginUrl="~/Account/Login" timeout="2880" />
    </authentication>
  </system.web>
</configuration>
```

**AFTER (.NET 8 - appsettings.json):**
```json
{
  "ConnectionStrings": {
    "DefaultConnection": "Server=localhost;Database=MyDb;Trusted_Connection=True;"
  },
  "AppSettings": {
    "AppName": "MyApp",
    "MaxUploadSize": 10485760
  },
  "Logging": {
    "LogLevel": {
      "Default": "Information",
      "Microsoft.AspNetCore": "Warning"
    }
  },
  "AllowedHosts": "*"
}
```

**Accessing in code:**

**BEFORE (.NET Framework):**
```csharp
using System.Configuration;

var connString = ConfigurationManager.ConnectionStrings["DefaultConnection"].ConnectionString;
var appName = ConfigurationManager.AppSettings["AppName"];
```

**AFTER (.NET 8):**
```csharp
using Microsoft.Extensions.Configuration;

public class MyService
{
    private readonly IConfiguration _config;
    
    public MyService(IConfiguration config)
    {
        _config = config;
    }
    
    public void DoSomething()
    {
        var connString = _config.GetConnectionString("DefaultConnection");
        var appName = _config["AppSettings:AppName"];
    }
}
```

**Specific changes:**
1. Remove: `Web.config` file
2. Create: `appsettings.json` (and `appsettings.Development.json`, `appsettings.Production.json`)
3. Replace: `<connectionStrings>` XML → JSON `"ConnectionStrings": {}`
4. Replace: `<appSettings>` XML → JSON `"AppSettings": {}`
5. Remove: `<system.web>` (configuration moved to code)
6. Replace: `ConfigurationManager` → `IConfiguration` injected via DI
7. Add: Environment-specific config files (appsettings.{Environment}.json)

**Why**: .NET Core uses JSON-based configuration with built-in environment support and strongly-typed options.

---

### Pattern 5: Entity Framework 6 → Entity Framework Core

**BEFORE (.NET Framework - EF6):**
```csharp
using System.Data.Entity;

namespace MyApp.Data
{
    public class AppDbContext : DbContext
    {
        public AppDbContext() : base("DefaultConnection")
        {
        }
        
        public DbSet<User> Users { get; set; }
        public DbSet<Order> Orders { get; set; }
        
        protected override void OnModelCreating(DbModelBuilder modelBuilder)
        {
            modelBuilder.Entity<User>()
                .HasMany(u => u.Orders)
                .WithRequired(o => o.User)
                .HasForeignKey(o => o.UserId);
        }
    }
}
```

**AFTER (.NET 8 - EF Core):**
```csharp
using Microsoft.EntityFrameworkCore;

namespace MyApp.Data
{
    public class AppDbContext : DbContext
    {
        public AppDbContext(DbContextOptions<AppDbContext> options)
            : base(options)
        {
        }
        
        public DbSet<User> Users { get; set; }
        public DbSet<Order> Orders { get; set; }
        
        protected override void OnModelCreating(ModelBuilder modelBuilder)
        {
            modelBuilder.Entity<User>()
                .HasMany(u => u.Orders)
                .WithOne(o => o.User)
                .HasForeignKey(o => o.UserId);
        }
    }
}
```

**Program.cs registration:**
```csharp
builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlServer(builder.Configuration.GetConnectionString("DefaultConnection")));
```

**Specific changes:**
1. Replace: `using System.Data.Entity` → `using Microsoft.EntityFrameworkCore`
2. Replace: Parameterless constructor → `DbContextOptions<T>` constructor
3. Replace: `DbModelBuilder` → `ModelBuilder`
4. Replace: `.WithRequired()` → `.WithOne()`
5. Add: DbContext registration in `Program.cs` with connection string
6. Remove: Connection string name from constructor (passed via DI)

**Why**: EF Core is a rewrite with better performance, cross-platform support, and built-in DI.

---

### Pattern 6: Custom HTTP Module → Middleware

**BEFORE (.NET Framework - HTTP Module in Web.config):**
```csharp
using System;
using System.Web;

namespace MyApp.Modules
{
    public class LoggingModule : IHttpModule
    {
        public void Init(HttpApplication context)
        {
            context.BeginRequest += Context_BeginRequest;
            context.EndRequest += Context_EndRequest;
        }
        
        private void Context_BeginRequest(object sender, EventArgs e)
        {
            var app = (HttpApplication)sender;
            var path = app.Request.Path;
            // Log request
        }
        
        private void Context_EndRequest(object sender, EventArgs e)
        {
            // Log response
        }
        
        public void Dispose() { }
    }
}
```

```xml
<!-- Web.config -->
<system.webServer>
  <modules>
    <add name="LoggingModule" type="MyApp.Modules.LoggingModule" />
  </modules>
</system.webServer>
```

**AFTER (.NET 8 - Middleware):**
```csharp
using Microsoft.AspNetCore.Http;
using System.Threading.Tasks;

namespace MyApp.Middleware
{
    public class LoggingMiddleware
    {
        private readonly RequestDelegate _next;
        
        public LoggingMiddleware(RequestDelegate next)
        {
            _next = next;
        }
        
        public async Task InvokeAsync(HttpContext context)
        {
            // Log request
            var path = context.Request.Path;
            
            await _next(context);
            
            // Log response
        }
    }
}
```

**Program.cs:**
```csharp
app.UseMiddleware<LoggingMiddleware>();
// OR inline:
app.Use(async (context, next) =>
{
    // Before request
    await next();
    // After response
});
```

**Specific changes:**
1. Remove: `IHttpModule` interface, `Web.config` registration
2. Create: Middleware class with `RequestDelegate` + `InvokeAsync(HttpContext)`
3. Replace: `BeginRequest` → code before `await _next(context)`
4. Replace: `EndRequest` → code after `await _next(context)`
5. Add: `app.UseMiddleware<T>()` in `Program.cs`

**Why**: ASP.NET Core uses async middleware pipeline instead of event-based HTTP modules.

---

## Files to DELETE

| Delete this | Replaced by | Reason |
|-------------|-------------|--------|
| `Web.config` | `appsettings.json` | Configuration moved to JSON |
| `Global.asax` / `Global.asax.cs` | `Program.cs` / `Startup.cs` | Application startup redesigned |
| `packages.config` | PackageReference in `.csproj` | NuGet moved to SDK-style projects |
| `App_Start/*.cs` (RouteConfig, FilterConfig, BundleConfig) | `Program.cs` middleware | Configuration now in code |
| `*.cshtml` in `App_Start` | N/A | Bundling moved to external tools |

---

## Files to CREATE

| File | Purpose | Template |
|------|---------|----------|
| `appsettings.json` | Application settings | See Pattern 4 |
| `appsettings.Development.json` | Dev-specific settings | Override settings for dev |
| `Program.cs` | Application entry point | See Pattern 3 |
| `Startup.cs` (optional) | Startup configuration | See Pattern 3 (traditional style) |

---

## Build File Changes

### .csproj (SDK-style)

**BEFORE (.NET Framework 4.x - legacy .csproj):**
```xml
<?xml version="1.0" encoding="utf-8"?>
<Project ToolsVersion="15.0" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <PropertyGroup>
    <TargetFrameworkVersion>v4.8</TargetFrameworkVersion>
  </PropertyGroup>
  <ItemGroup>
    <Reference Include="System.Web" />
    <Reference Include="System.Web.Mvc, Version=5.2.7.0" />
  </ItemGroup>
  <ItemGroup>
    <Compile Include="Controllers\HomeController.cs" />
    <Compile Include="Global.asax.cs">
      <DependentUpon>Global.asax</DependentUpon>
    </Compile>
  </ItemGroup>
</Project>
```

**AFTER (.NET 8 - SDK-style .csproj):**
```xml
<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <Nullable>enable</Nullable>
    <ImplicitUsings>enable</ImplicitUsings>
  </PropertyGroup>
  
  <ItemGroup>
    <PackageReference Include="Microsoft.EntityFrameworkCore.SqlServer" Version="8.0.0" />
    <PackageReference Include="Microsoft.EntityFrameworkCore.Tools" Version="8.0.0" />
  </ItemGroup>
</Project>
```

**Specific changes:**
1. Replace: `<Project ToolsVersion=...>` → `<Project Sdk="Microsoft.NET.Sdk.Web">`
2. Replace: `<TargetFrameworkVersion>v4.8</TargetFrameworkVersion>` → `<TargetFramework>net8.0</TargetFramework>`
3. Remove: All `<Reference>` entries (implicit with SDK)
4. Remove: All `<Compile>` entries (auto-discovered by SDK)
5. Remove: `packages.config` — use `<PackageReference>` instead
6. Add: `<Nullable>enable</Nullable>` (recommended for new projects)
7. Add: `<ImplicitUsings>enable</ImplicitUsings>` (C# 10+ feature)

---

## Verification Commands

```bash
# 1. Build the project
dotnet build

# 2. Check for System.Web references (should be 0)
grep -rn "using System.Web" . --include="*.cs" | wc -l
# Should be 0

# 3. Check target framework in .csproj
grep "TargetFramework" *.csproj
# Should show net8.0 or net7.0

# 4. Run tests
dotnet test

# 5. Run the application
dotnet run

# 6. Check application URL
# Default: https://localhost:5001 or http://localhost:5000

# 7. Verify appsettings.json is loaded
# Check logs for "Now listening on: https://localhost:5001"
```

---

## Notes / Gotchas

### 1. **Web Forms NOT Supported**
ASP.NET Web Forms (`.aspx` files) have **no direct equivalent** in ASP.NET Core. 

**Options**:
- Rewrite as Razor Pages (similar page-based model)
- Rewrite as MVC (if complex logic)
- Use Blazor (component-based, similar to Web Forms)
- Keep Web Forms app on .NET Framework 4.8 (LTS until 2028)

### 2. **Session State Requires Package**
Session state is not included by default.

**Add package**:
```bash
dotnet add package Microsoft.AspNetCore.Session
```

**Enable in Program.cs**:
```csharp
builder.Services.AddDistributedMemoryCache();
builder.Services.AddSession(options =>
{
    options.IdleTimeout = TimeSpan.FromMinutes(30);
});

app.UseSession();  // Before UseRouting()
```

### 3. **Bundling and Minification**
`System.Web.Optimization` is not available. Use external tools:
- **Webpack** (most common)
- **Vite** (modern, fast)
- **Gulp/Grunt** (older, still used)
- **LibMan** (simple, built-in for ASP.NET Core)

**LibMan example** (appsettings.json):
```json
{
  "provider": "cdnjs",
  "library": "jquery@3.6.0",
  "destination": "wwwroot/lib/jquery/"
}
```

### 4. **CORS Must Be Configured**
If building an API, enable CORS explicitly:

```csharp
builder.Services.AddCors(options =>
{
    options.AddDefaultPolicy(policy =>
    {
        policy.AllowAnyOrigin()
              .AllowAnyMethod()
              .AllowAnyHeader();
    });
});

app.UseCors();  // Before UseAuthorization()
```

### 5. **Authentication Changes**
ASP.NET Identity works differently. For Forms Authentication → ASP.NET Core Identity:

**Add packages**:
```bash
dotnet add package Microsoft.AspNetCore.Identity.EntityFrameworkCore
```

**Register in Program.cs**:
```csharp
builder.Services.AddIdentity<ApplicationUser, IdentityRole>()
    .AddEntityFrameworkStores<AppDbContext>()
    .AddDefaultTokenProviders();

app.UseAuthentication();
app.UseAuthorization();
```

### 6. **Static Files in wwwroot**
Move all static files (CSS, JS, images) from `Content/`, `Scripts/`, `Images/` to `wwwroot/`:

```
BEFORE:
  Content/
    site.css
  Scripts/
    site.js

AFTER:
  wwwroot/
    css/
      site.css
    js/
      site.js
```

### 7. **Razor View Syntax Changes**
Mostly compatible, but:
- `@Html.AntiForgeryToken()` still works
- `@Url.Action()` still works
- `@Html.BeginForm()` still works

**New**: Tag Helpers (recommended)
```html
<!-- OLD -->
@Html.TextBoxFor(m => m.Name, new { @class = "form-control" })

<!-- NEW (Tag Helpers) -->
<input asp-for="Name" class="form-control" />
```

### 8. **Dependency Injection is Mandatory**
Unlike .NET Framework (optional), DI is **built-in and required** in .NET Core.

**ALL services must be registered**:
```csharp
builder.Services.AddScoped<IUserService, UserService>();
builder.Services.AddSingleton<ICacheService, CacheService>();
builder.Services.AddTransient<IEmailService, EmailService>();
```

### 9. **Async All the Way**
ASP.NET Core is designed for async. Update synchronous code to async:

```csharp
// OLD
public ActionResult Index()
{
    var users = _userService.GetAll();
    return View(users);
}

// NEW (preferred)
public async Task<IActionResult> Index()
{
    var users = await _userService.GetAllAsync();
    return View(users);
}
```

### 10. **Hosting Models**
ASP.NET Core can run:
- **Kestrel** (cross-platform, default)
- **IIS** (Windows, in-process or out-of-process)
- **Docker** (containerized)
- **Self-hosted** (console app)

**IIS hosting** requires `web.config` (auto-generated):
```xml
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <location path="." inheritInChildApplications="false">
    <system.webServer>
      <handlers>
        <add name="aspNetCore" path="*" verb="*" modules="AspNetCoreModuleV2" />
      </handlers>
      <aspNetCore processPath="dotnet" arguments=".\MyApp.dll" stdoutLogEnabled="false" />
    </system.webServer>
  </location>
</configuration>
```

---

## Migration Checklist

- [ ] Convert .csproj to SDK-style (`<Project Sdk="Microsoft.NET.Sdk.Web">`)
- [ ] Update TargetFramework to `net8.0`
- [ ] Replace all `System.Web.*` → `Microsoft.AspNetCore.*`
- [ ] Create `appsettings.json` and migrate Web.config settings
- [ ] Create `Program.cs` and migrate Global.asax logic
- [ ] Update controllers (`ActionResult` → `IActionResult`, `ApiController` → `ControllerBase`)
- [ ] Migrate Entity Framework 6 → EF Core (if used)
- [ ] Convert HTTP modules to middleware
- [ ] Move static files to `wwwroot/`
- [ ] Set up dependency injection for all services
- [ ] Update authentication/authorization (if used)
- [ ] Test all endpoints and views
- [ ] Delete `Web.config`, `Global.asax`, `packages.config`, `App_Start/`

---

## Resources

- [.NET Upgrade Assistant](https://dotnet.microsoft.com/en-us/platform/upgrade-assistant) - Automated migration tool
- [ASP.NET Core Migration Guide](https://learn.microsoft.com/en-us/aspnet/core/migration/proper-to-2x/)
- [EF6 to EF Core Migration](https://learn.microsoft.com/en-us/ef/efcore-and-ef6/porting/)
- [.NET 8 Documentation](https://learn.microsoft.com/en-us/dotnet/core/whats-new/dotnet-8)
- [ASP.NET Core Fundamentals](https://learn.microsoft.com/en-us/aspnet/core/fundamentals/)
